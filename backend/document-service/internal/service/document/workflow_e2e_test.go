package document

import (
	"context"
	"database/sql"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"backend-document-service/internal/config"
	"backend-document-service/internal/grpcdial"
	"backend-document-service/internal/model"
	githubpublisherv1 "backend-document-service/proto/backend/githubpublisher/v1"
	storagev1 "backend-document-service/proto/backend/storage/v1"
	transformerv1 "backend-document-service/proto/backend/transformer/v1"

	"github.com/google/uuid"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/worker"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/test/bufconn"
)

func TestDocumentWorkflowEndToEnd(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	if _, err := os.Stat("/var/run/docker.sock"); err != nil {
		t.Skip("docker socket is unavailable, skipping temporalite-based e2e test")
	}

	temporal, err := startTemporalite(ctx)
	if err != nil {
		t.Fatalf("start temporalite: %v", err)
	}
	defer func() {
		_ = temporal.Terminate(ctx)
	}()

	host, err := temporal.Host(ctx)
	if err != nil {
		t.Fatalf("get host: %v", err)
	}
	port, err := temporal.MappedPort(ctx, "7233")
	if err != nil {
		t.Fatalf("get port: %v", err)
	}

	clientOpts := client.Options{HostPort: fmt.Sprintf("%s:%s", host, port.Port()), Namespace: "knowledge-base"}
	temporalClient, err := client.Dial(clientOpts)
	if err != nil {
		t.Fatalf("dial temporal: %v", err)
	}
	defer temporalClient.Close()

	storageRecorder := &metadataRecorder{}
	storageConn := startBufConnServer(t, func(s *grpc.Server) {
		storagev1.RegisterStorageServiceServer(s, &fakeStorageServer{recorder: storageRecorder})
	})
	defer storageConn.Close()

	transformerRecorder := &metadataRecorder{}
	transformerConn := startBufConnServer(t, func(s *grpc.Server) {
		transformerv1.RegisterTransformerServiceServer(s, &fakeTransformerServer{recorder: transformerRecorder})
	})
	defer transformerConn.Close()

	publisherRecorder := &metadataRecorder{}
	publisherServer := &fakePublisherServer{recorder: publisherRecorder}
	publisherConn := startBufConnServer(t, func(s *grpc.Server) {
		githubpublisherv1.RegisterGithubPublisherServiceServer(s, publisherServer)
	})
	defer publisherConn.Close()

	githubServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer githubServer.Close()

	docID := uuid.NewString()
	repo := &memoryRepo{doc: &model.Document{
		ID:                docID,
		Name:              "Document title",
		Owner:             "custom-owner",
		Repository:        "custom-repo",
		Branch:            "custom-branch",
		BaseBranch:        "main",
		Path:              "docs/key.md",
		WorkflowID:        docID,
		Status:            model.StatusUploaded,
		OriginalBucket:    "orig",
		OriginalObjectKey: "orig/key.docx",
		MarkdownBucket:    "md",
		MarkdownObjectKey: "md/key.md",
		Versions:          []model.DocumentVersion{{Version: 1, OriginalObjectKey: "orig/key.docx", MarkdownObjectKey: "md/key.md"}},
		CreatePR:          true,
	}}

	activities := NewActivities(repo, storagev1.NewStorageServiceClient(storageConn), transformerv1.NewTransformerServiceClient(transformerConn), githubpublisherv1.NewGithubPublisherServiceClient(publisherConn), "owner", "repo", "main", "main", githubServer.URL)
	activities.httpClient = githubServer.Client()
	workerInstance := worker.New(temporalClient, "document-processing", worker.Options{})
	workerInstance.RegisterWorkflow(DocumentProcessingWorkflow)
	workerInstance.RegisterActivityWithOptions(activities.AwaitOriginal, activity.RegisterOptions{Name: ActivityAwaitOriginalName})
	workerInstance.RegisterActivityWithOptions(activities.UploadOriginal, activity.RegisterOptions{Name: ActivityUploadOriginalName})
	workerInstance.RegisterActivityWithOptions(activities.ConvertToMarkdown, activity.RegisterOptions{Name: ActivityConvertToMarkdownName})
	workerInstance.RegisterActivityWithOptions(activities.AwaitMarkdown, activity.RegisterOptions{Name: ActivityAwaitMarkdownName})
	workerInstance.RegisterActivityWithOptions(activities.PublishMarkdown, activity.RegisterOptions{Name: ActivityPublishMarkdownName})

	go func() {
		_ = workerInstance.Run(worker.InterruptCh())
	}()

	input := WorkflowInput{DocumentID: docID}
	options := client.StartWorkflowOptions{ID: input.DocumentID, TaskQueue: "document-processing"}
	run, err := temporalClient.ExecuteWorkflow(ctx, options, DocumentProcessingWorkflow, input)
	if err != nil {
		t.Fatalf("start workflow: %v", err)
	}
	if err := run.Get(ctx, nil); err != nil {
		t.Fatalf("workflow result: %v", err)
	}

	if storageRecorder.firstCorrelationID() == "" {
		t.Fatalf("correlation id was not propagated to storage service")
	}
	if transformerRecorder.firstCorrelationID() == "" {
		t.Fatalf("correlation id was not propagated to transformer service")
	}
	if publisherRecorder.firstCorrelationID() == "" {
		t.Fatalf("correlation id was not propagated to publisher service")
	}
	if publisherServer.lastCorrelationID == "" {
		t.Fatalf("correlation id was not propagated inside request")
	}
	if publisherServer.lastCorrelationID != input.DocumentID {
		t.Fatalf("unexpected correlation id: %s", publisherServer.lastCorrelationID)
	}
	if publisherServer.lastOwner != "custom-owner" || publisherServer.lastRepository != "custom-repo" || publisherServer.lastBranch != "custom-branch" || publisherServer.lastBaseBranch != "main" || publisherServer.lastPath != "docs/key.md" {
		t.Fatalf("unexpected publish request: owner=%s repo=%s branch=%s base=%s path=%s", publisherServer.lastOwner, publisherServer.lastRepository, publisherServer.lastBranch, publisherServer.lastBaseBranch, publisherServer.lastPath)
	}
	if !publisherServer.lastCreatePR {
		t.Fatalf("create_pr flag was not propagated")
	}
	if publisherServer.lastBucket != "md" || publisherServer.lastObjectKey != "md/key.md" {
		t.Fatalf("bucket/object not passed: %s/%s", publisherServer.lastBucket, publisherServer.lastObjectKey)
	}
	if publisherServer.lastCommitMessage != repo.doc.Name {
		t.Fatalf("unexpected commit message: %s", publisherServer.lastCommitMessage)
	}
}

func TestDocumentWorkflowRetrySkipsCompletedSteps(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	if _, err := os.Stat("/var/run/docker.sock"); err != nil {
		t.Skip("docker socket is unavailable, skipping temporalite-based e2e test")
	}

	temporal, err := startTemporalite(ctx)
	if err != nil {
		t.Fatalf("start temporalite: %v", err)
	}
	defer func() {
		_ = temporal.Terminate(context.Background())
	}()

	host, err := temporal.Host(ctx)
	if err != nil {
		t.Fatalf("get host: %v", err)
	}
	port, err := temporal.MappedPort(ctx, "7233")
	if err != nil {
		t.Fatalf("get port: %v", err)
	}

	clientOpts := client.Options{HostPort: fmt.Sprintf("%s:%s", host, port.Port()), Namespace: "knowledge-base"}
	temporalClient, err := client.Dial(clientOpts)
	if err != nil {
		t.Fatalf("dial temporal: %v", err)
	}
	defer temporalClient.Close()

	storageRecorder := &metadataRecorder{}
	storageConn := startBufConnServer(t, func(s *grpc.Server) {
		storagev1.RegisterStorageServiceServer(s, &fakeStorageServer{recorder: storageRecorder})
	})
	defer storageConn.Close()

	transformerRecorder := &metadataRecorder{}
	transformerConn := startBufConnServer(t, func(s *grpc.Server) {
		transformerv1.RegisterTransformerServiceServer(s, &fakeTransformerServer{recorder: transformerRecorder})
	})
	defer transformerConn.Close()

	publisherRecorder := &metadataRecorder{}
	publisherServer := &fakePublisherServer{recorder: publisherRecorder, failuresLeft: 1}
	publisherConn := startBufConnServer(t, func(s *grpc.Server) {
		githubpublisherv1.RegisterGithubPublisherServiceServer(s, publisherServer)
	})
	defer publisherConn.Close()

	githubServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer githubServer.Close()

	docID := uuid.NewString()
	repo := &memoryRepo{doc: &model.Document{
		ID:                docID,
		Name:              "Document title",
		Owner:             "custom-owner",
		Repository:        "custom-repo",
		Branch:            "custom-branch",
		BaseBranch:        "main",
		Path:              "docs/key.md",
		WorkflowID:        docID,
		Status:            model.StatusUploaded,
		OriginalBucket:    "orig",
		OriginalObjectKey: "orig/key.docx",
		MarkdownBucket:    "md",
		MarkdownObjectKey: "md/key.md",
		Versions:          []model.DocumentVersion{{Version: 1, OriginalObjectKey: "orig/key.docx", MarkdownObjectKey: "md/key.md"}},
		CreatePR:          true,
	}, logs: []model.Status{model.StatusUploaded}}

	activities := NewActivities(repo, storagev1.NewStorageServiceClient(storageConn), transformerv1.NewTransformerServiceClient(transformerConn), githubpublisherv1.NewGithubPublisherServiceClient(publisherConn), "owner", "repo", "main", "main", githubServer.URL)
	activities.httpClient = githubServer.Client()
	workerInstance := worker.New(temporalClient, "document-processing", worker.Options{})
	workerInstance.RegisterWorkflow(DocumentProcessingWorkflow)
	workerInstance.RegisterActivityWithOptions(activities.AwaitOriginal, activity.RegisterOptions{Name: ActivityAwaitOriginalName})
	workerInstance.RegisterActivityWithOptions(activities.UploadOriginal, activity.RegisterOptions{Name: ActivityUploadOriginalName})
	workerInstance.RegisterActivityWithOptions(activities.ConvertToMarkdown, activity.RegisterOptions{Name: ActivityConvertToMarkdownName})
	workerInstance.RegisterActivityWithOptions(activities.AwaitMarkdown, activity.RegisterOptions{Name: ActivityAwaitMarkdownName})
	workerInstance.RegisterActivityWithOptions(activities.PublishMarkdown, activity.RegisterOptions{Name: ActivityPublishMarkdownName})

	go func() {
		_ = workerInstance.Run(worker.InterruptCh())
	}()
	defer workerInstance.Stop()

	input := WorkflowInput{DocumentID: docID}
	options := client.StartWorkflowOptions{ID: docID, TaskQueue: "document-processing"}

	run, err := temporalClient.ExecuteWorkflow(ctx, options, DocumentProcessingWorkflow, input)
	if err != nil {
		t.Fatalf("start workflow: %v", err)
	}
	if err := run.Get(ctx, nil); err == nil {
		t.Fatalf("workflow should fail on first publish attempt")
	}

	run, err = temporalClient.ExecuteWorkflow(ctx, options, DocumentProcessingWorkflow, input)
	if err != nil {
		t.Fatalf("start workflow: %v", err)
	}
	if err := run.Get(ctx, nil); err != nil {
		t.Fatalf("workflow result after retry: %v", err)
	}

	if len(transformerRecorder.values) != 1 {
		t.Fatalf("expected single conversion, got %d", len(transformerRecorder.values))
	}
	if len(storageRecorder.values) != 1 {
		t.Fatalf("expected single original fetch, got %d", len(storageRecorder.values))
	}
	if len(publisherRecorder.values) != 2 {
		t.Fatalf("expected two publish attempts, got %d", len(publisherRecorder.values))
	}
	if repo.lastStatus != model.StatusPublishedToGitHub {
		t.Fatalf("unexpected final status: %s", repo.lastStatus)
	}
}

func startTemporalite(ctx context.Context) (testcontainers.Container, error) {
	return testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			Image:        "temporalio/temporalite:latest",
			ExposedPorts: []string{"7233/tcp"},
			Cmd:          []string{"start", "--ip", "0.0.0.0", "--port", "7233", "--namespace", "knowledge-base", "--headless"},
			WaitingFor:   wait.ForListeningPort("7233/tcp").WithStartupTimeout(30 * time.Second),
		},
		Started: true,
	})
}

func testGRPCConfig() config.GRPCClient {
	return config.GRPCClient{
		DialTimeout:       time.Second,
		MinConnectTimeout: 500 * time.Millisecond,
		Backoff: config.Backoff{
			BaseDelay:  100 * time.Millisecond,
			Multiplier: 1.6,
			Jitter:     0.2,
			MaxDelay:   time.Second,
		},
		Keepalive: config.Keepalive{
			Time:                10 * time.Second,
			Timeout:             3 * time.Second,
			PermitWithoutStream: true,
		},
	}
}

func startBufConnServer(t *testing.T, register func(*grpc.Server)) *grpc.ClientConn {
	t.Helper()
	lis := bufconn.Listen(1024 * 1024)
	server := grpc.NewServer()
	register(server)
	go func() {
		_ = server.Serve(lis)
	}()
	conn, err := grpcdial.DialContext(
		context.Background(),
		"bufnet",
		testGRPCConfig(),
		grpcdial.WithDialOptions(grpc.WithContextDialer(func(ctx context.Context, s string) (net.Conn, error) {
			return lis.Dial()
		})),
		grpcdial.WithoutBlock(),
	)
	if err != nil {
		t.Fatalf("dial bufconn: %v", err)
	}
	return conn
}

type metadataRecorder struct {
	values []string
}

func (m *metadataRecorder) record(ctx context.Context) {
	if md, ok := metadata.FromIncomingContext(ctx); ok {
		if corr := md.Get(correlationIDHeader); len(corr) > 0 {
			m.values = append(m.values, corr[0])
		}
	}
}

func (m *metadataRecorder) firstCorrelationID() string {
	if len(m.values) == 0 {
		return ""
	}
	return m.values[0]
}

type fakeStorageServer struct {
	storagev1.UnimplementedStorageServiceServer
	recorder *metadataRecorder
}

func (f *fakeStorageServer) UploadOriginal(ctx context.Context, req *storagev1.UploadOriginalRequest) (*storagev1.UploadObjectResponse, error) {
	f.recorder.record(ctx)
	return &storagev1.UploadObjectResponse{Bucket: "orig", ObjectKey: fmt.Sprintf("%s/%s", "orig", req.GetDocumentId())}, nil
}

func (f *fakeStorageServer) UploadMarkdown(ctx context.Context, req *storagev1.UploadMarkdownRequest) (*storagev1.UploadObjectResponse, error) {
	f.recorder.record(ctx)
	return &storagev1.UploadObjectResponse{Bucket: "md", ObjectKey: fmt.Sprintf("md/%s", req.GetDocumentId())}, nil
}

func (f *fakeStorageServer) GetObject(ctx context.Context, req *storagev1.GetObjectRequest) (*storagev1.GetObjectResponse, error) {
	f.recorder.record(ctx)
	return &storagev1.GetObjectResponse{Bucket: req.GetBucket(), ObjectKey: req.GetObjectKey()}, nil
}

func (f *fakeStorageServer) GenerateUploadURL(context.Context, *storagev1.GenerateUploadURLRequest) (*storagev1.GenerateUploadURLResponse, error) {
	return nil, fmt.Errorf("not implemented")
}

type fakeTransformerServer struct {
	transformerv1.UnimplementedTransformerServiceServer
	recorder *metadataRecorder
}

func (f *fakeTransformerServer) ConvertDocumentToMarkdown(ctx context.Context, req *transformerv1.ConvertDocumentToMarkdownRequest) (*transformerv1.ConvertDocumentToMarkdownResponse, error) {
	f.recorder.record(ctx)
	return &transformerv1.ConvertDocumentToMarkdownResponse{QueueId: "queue-123", Status: "pending", MarkdownObjectKey: req.GetMarkdownObjectKey(), MarkdownBucket: req.GetMarkdownBucket()}, nil
}

type fakePublisherServer struct {
	githubpublisherv1.UnimplementedGithubPublisherServiceServer
	recorder          *metadataRecorder
	lastOwner         string
	lastRepository    string
	lastBranch        string
	lastBaseBranch    string
	lastPath          string
	lastCommitMessage string
	lastCorrelationID string
	lastBucket        string
	lastObjectKey     string
	lastCreatePR      bool
	failuresLeft      int
}

func (f *fakePublisherServer) PublishMarkdown(ctx context.Context, req *githubpublisherv1.PublishMarkdownRequest) (*githubpublisherv1.PublishMarkdownResponse, error) {
	f.recorder.record(ctx)
	f.lastOwner = req.GetOwner()
	f.lastRepository = req.GetRepository()
	f.lastBranch = req.GetBranch()
	f.lastBaseBranch = req.GetBaseBranch()
	f.lastPath = req.GetPath()
	f.lastCommitMessage = req.GetCommitMessage()
	f.lastCorrelationID = req.GetCorrelationId()
	f.lastBucket = req.GetBucket()
	f.lastObjectKey = req.GetObjectKey()
	f.lastCreatePR = req.GetCreatePr()
	if req.GetContent() == "" && (req.GetBucket() == "" || req.GetObjectKey() == "") {
		return nil, fmt.Errorf("empty content")
	}
	if f.failuresLeft > 0 {
		f.failuresLeft--
		return nil, fmt.Errorf("temporary publish error")
	}
	return &githubpublisherv1.PublishMarkdownResponse{CommitSha: "commit-sha", PullRequestNumber: 42, PullRequestUrl: "https://example.com/pr/42"}, nil
}

type memoryRepo struct {
	lastStatus model.Status
	workflowID string
	lastError  string
	doc        *model.Document
	logs       []model.Status
}

func (m *memoryRepo) CreateDocument(context.Context, *model.Document, *model.DocumentVersion, *model.DocumentStatusLog) (*model.Document, error) {
	return nil, nil
}

func (m *memoryRepo) GetDocument(_ context.Context, id string) (*model.Document, error) {
	if m.doc != nil && m.doc.ID == id {
		return m.doc, nil
	}
	return nil, fmt.Errorf("not found")
}

func (m *memoryRepo) ListDocuments(context.Context, int, int, *model.Status) ([]model.Document, error) {
	return nil, nil
}

func (m *memoryRepo) CountStatusLogsWithReasonPrefix(context.Context, string, model.Status, string) (int, error) {
	return len(m.logs), nil
}

func (m *memoryRepo) UpdateStatus(ctx context.Context, id string, to model.Status, reason, errorReason string, version *model.DocumentVersion) (*model.Document, error) {
	m.lastStatus = to
	m.lastError = errorReason
	if m.doc == nil || m.doc.ID != id {
		return &model.Document{ID: id, Status: to}, nil
	}
	m.doc.Status = to
	m.doc.ErrorReason = errorReason
	if version != nil {
		m.doc.Versions = append(m.doc.Versions, *version)
	}
	m.logs = append(m.logs, to)
	return m.doc, nil
}

func (m *memoryRepo) LastNonFailedStatus(ctx context.Context, id string) (model.Status, error) {
	if m.doc == nil || m.doc.ID != id {
		return "", fmt.Errorf("not found")
	}
	for i := len(m.logs) - 1; i >= 0; i-- {
		if m.logs[i] != model.StatusFailed {
			return m.logs[i], nil
		}
	}
	return "", sql.ErrNoRows
}

func (m *memoryRepo) UpdateWorkflowID(ctx context.Context, id, workflowID string) (*model.Document, error) {
	m.workflowID = workflowID
	if m.doc != nil && m.doc.ID == id {
		m.doc.WorkflowID = workflowID
		return m.doc, nil
	}
	return &model.Document{ID: id, WorkflowID: workflowID}, nil
}
