package document

import (
	"context"
	"fmt"
	"net"
	"os"
	"testing"
	"time"

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
	publisherConn := startBufConnServer(t, func(s *grpc.Server) {
		githubpublisherv1.RegisterGithubPublisherServiceServer(s, &fakePublisherServer{recorder: publisherRecorder})
	})
	defer publisherConn.Close()

	activities := NewActivities(&memoryRepo{}, storagev1.NewStorageServiceClient(storageConn), transformerv1.NewTransformerServiceClient(transformerConn), githubpublisherv1.NewGithubPublisherServiceClient(publisherConn), "repo", "main")
	workerInstance := worker.New(temporalClient, "document-processing", worker.Options{})
	workerInstance.RegisterWorkflow(DocumentProcessingWorkflow)
	workerInstance.RegisterActivityWithOptions(activities.UploadOriginal, activity.RegisterOptions{Name: ActivityUploadOriginalName})
	workerInstance.RegisterActivityWithOptions(activities.ConvertToMarkdown, activity.RegisterOptions{Name: ActivityConvertToMarkdownName})
	workerInstance.RegisterActivityWithOptions(activities.PublishMarkdown, activity.RegisterOptions{Name: ActivityPublishMarkdownName})

	go func() {
		_ = workerInstance.Run(worker.InterruptCh())
	}()

	input := WorkflowInput{
		DocumentID:        uuid.NewString(),
		OriginalBucket:    "orig",
		MarkdownBucket:    "md",
		OriginalObjectKey: "orig/key.docx",
		MarkdownObjectKey: "md/key.md",
		FilenameExt:       ".docx",
		PublishPath:       "docs/key.md",
		OriginalContent:   []byte("content"),
		Version:           1,
	}
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

func startBufConnServer(t *testing.T, register func(*grpc.Server)) *grpc.ClientConn {
	t.Helper()
	lis := bufconn.Listen(1024 * 1024)
	server := grpc.NewServer()
	register(server)
	go func() {
		_ = server.Serve(lis)
	}()
	conn, err := grpc.DialContext(context.Background(), "bufnet", grpc.WithContextDialer(func(ctx context.Context, s string) (net.Conn, error) {
		return lis.Dial()
	}), grpc.WithInsecure())
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

func (f *fakeStorageServer) GetObject(context.Context, *storagev1.GetObjectRequest) (*storagev1.GetObjectResponse, error) {
	return nil, fmt.Errorf("not implemented")
}

func (f *fakeStorageServer) GenerateUploadURL(context.Context, *storagev1.GenerateUploadURLRequest) (*storagev1.GenerateUploadURLResponse, error) {
	return nil, fmt.Errorf("not implemented")
}

type fakeTransformerServer struct {
	transformerv1.UnimplementedTransformerServiceServer
	recorder *metadataRecorder
}

func (f *fakeTransformerServer) ConvertDocxToMarkdown(ctx context.Context, req *transformerv1.ConvertDocxToMarkdownRequest) (*transformerv1.ConvertDocxToMarkdownResponse, error) {
	f.recorder.record(ctx)
	return &transformerv1.ConvertDocxToMarkdownResponse{MarkdownContent: []byte("markdown"), MarkdownObjectKey: req.GetMarkdownObjectKey(), MediaType: "text/markdown"}, nil
}

type fakePublisherServer struct {
	githubpublisherv1.UnimplementedGithubPublisherServiceServer
	recorder *metadataRecorder
}

func (f *fakePublisherServer) PublishMarkdown(ctx context.Context, req *githubpublisherv1.PublishMarkdownRequest) (*githubpublisherv1.PublishMarkdownResponse, error) {
	f.recorder.record(ctx)
	if len(req.GetContent()) == 0 {
		return nil, fmt.Errorf("empty content")
	}
	return &githubpublisherv1.PublishMarkdownResponse{}, nil
}

type memoryRepo struct {
	lastStatus model.Status
	workflowID string
}

func (m *memoryRepo) CreateDocument(context.Context, *model.Document, *model.DocumentVersion, *model.DocumentStatusLog) (*model.Document, error) {
	return nil, nil
}

func (m *memoryRepo) GetDocument(context.Context, string) (*model.Document, error) { return nil, nil }

func (m *memoryRepo) ListDocuments(context.Context, int, int, *model.Status) ([]model.Document, error) {
	return nil, nil
}

func (m *memoryRepo) UpdateStatus(ctx context.Context, id string, to model.Status, reason string, version *model.DocumentVersion) (*model.Document, error) {
	m.lastStatus = to
	return &model.Document{ID: id, Status: to}, nil
}

func (m *memoryRepo) UpdateWorkflowID(ctx context.Context, id, workflowID string) (*model.Document, error) {
	m.workflowID = workflowID
	return &model.Document{ID: id, WorkflowID: workflowID}, nil
}
