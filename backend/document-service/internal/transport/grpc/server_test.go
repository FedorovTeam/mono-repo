package grpc

import (
	"context"
	"database/sql"
	"errors"
	"net"
	"sync"
	"testing"
	"time"

	"backend-document-service/internal/config"
	"backend-document-service/internal/grpcdial"
	"backend-document-service/internal/model"
	"backend-document-service/internal/service/document"
	documentv1 "backend-document-service/proto/backend/document/v1"
	storagev1 "backend-document-service/proto/backend/storage/v1"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/test/bufconn"
)

const bufSize = 1024 * 1024

func dialer(server *grpc.Server) (*grpc.ClientConn, func(), error) {
	lis := bufconn.Listen(bufSize)
	go func() {
		_ = server.Serve(lis)
	}()
	conn, err := grpcdial.DialContext(
		context.Background(),
		"bufnet",
		grpcClientConfig(),
		grpcdial.WithDialOptions(grpc.WithContextDialer(func(ctx context.Context, s string) (net.Conn, error) {
			return lis.Dial()
		})),
		grpcdial.WithoutBlock(),
	)
	if err != nil {
		return nil, nil, err
	}
	cleanup := func() {
		conn.Close()
		server.Stop()
	}
	return conn, cleanup, nil
}

func grpcClientConfig() config.GRPCClient {
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

type memoryRepo struct {
	mu        sync.Mutex
	documents map[string]model.Document
	logs      map[string][]model.Status
	jobs      map[string]model.ProcessingJob
}

func (m *memoryRepo) CreateDocument(ctx context.Context, doc *model.Document, version *model.DocumentVersion, log *model.DocumentStatusLog) (*model.Document, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.documents == nil {
		m.documents = make(map[string]model.Document)
	}
	doc.ID = uuid.NewString()
	doc.Versions = []model.DocumentVersion{*version}
	m.documents[doc.ID] = *doc
	return doc, nil
}

func (m *memoryRepo) GetDocument(ctx context.Context, id string) (*model.Document, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	doc, ok := m.documents[id]
	if !ok {
		return nil, errors.New("not found")
	}
	return &doc, nil
}

func (m *memoryRepo) ListDocuments(ctx context.Context, limit, offset int, status *model.Status) ([]model.Document, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []model.Document
	for _, d := range m.documents {
		if status != nil && d.Status != *status {
			continue
		}
		out = append(out, d)
	}
	return out, nil
}

func (m *memoryRepo) CountStatusLogsWithReasonPrefix(ctx context.Context, id string, status model.Status, reasonPrefix string) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.logs[id]), nil
}

func (m *memoryRepo) UpdateStatus(ctx context.Context, id string, to model.Status, reason, errorReason string, version *model.DocumentVersion) (*model.Document, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	doc, ok := m.documents[id]
	if !ok {
		return nil, errors.New("not found")
	}
	if err := model.ValidateTransition(doc.Status, to); err != nil {
		return nil, err
	}
	doc.Status = to
	if errorReason != "" {
		doc.ErrorReason = errorReason
	}
	if version != nil {
		doc.Versions = append(doc.Versions, *version)
		doc.MarkdownObjectKey = version.MarkdownObjectKey
	}
	if m.logs == nil {
		m.logs = make(map[string][]model.Status)
	}
	m.logs[id] = append(m.logs[id], to)
	m.documents[id] = doc
	return &doc, nil
}

func (m *memoryRepo) UpdateWorkflowID(ctx context.Context, id, workflowID string) (*model.Document, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	doc, ok := m.documents[id]
	if !ok {
		return nil, errors.New("not found")
	}
	doc.WorkflowID = workflowID
	m.documents[id] = doc
	return &doc, nil
}

func (m *memoryRepo) CreateProcessingJob(ctx context.Context, job *model.ProcessingJob, event *model.JobEvent) (*model.ProcessingJob, error) {
	if job.TaskID == "" {
		job.TaskID = uuid.NewString()
	}
	if m.jobs == nil {
		m.jobs = make(map[string]model.ProcessingJob)
	}
	if job.Status == "" {
		job.Status = model.JobStatusPending
	}
	m.jobs[job.TaskID] = *job
	return job, nil
}

func (m *memoryRepo) UpdateProcessingJob(ctx context.Context, taskID string, status model.JobStatus, nextRunAt *time.Time, incrementAttempt bool, payload map[string]any, event *model.JobEvent) (*model.ProcessingJob, error) {
	job := model.ProcessingJob{TaskID: taskID, Status: status}
	if nextRunAt != nil {
		job.NextRunAt = *nextRunAt
	}
	if payload != nil {
		job.Payload = payload
	}
	if m.jobs == nil {
		m.jobs = make(map[string]model.ProcessingJob)
	}
	m.jobs[taskID] = job
	return &job, nil
}

func (m *memoryRepo) ActiveJob(ctx context.Context, documentID string) (*model.ProcessingJob, error) {
	for _, job := range m.jobs {
		if job.DocumentID != documentID {
			continue
		}
		if job.Status == model.JobStatusPending || job.Status == model.JobStatusInProgress {
			copy := job
			return &copy, nil
		}
	}
	return nil, nil
}

func (m *memoryRepo) LastNonFailedStatus(ctx context.Context, id string) (model.Status, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	doc, ok := m.documents[id]
	if !ok {
		return "", errors.New("not found")
	}
	history := m.logs[id]
	if len(history) == 0 {
		if doc.Status != model.StatusFailed {
			return doc.Status, nil
		}
		return "", sql.ErrNoRows
	}
	for i := len(history) - 1; i >= 0; i-- {
		if history[i] != model.StatusFailed {
			return history[i], nil
		}
	}
	return "", sql.ErrNoRows
}

func TestGRPCWorkflow(t *testing.T) {
	repo := &memoryRepo{}
	storage := &noopStorageClient{}
	svc := document.New(repo, nil, storage, "orig", "md", "owner", "repo", "main", "main")
	srv := NewServer(svc)
	grpcServer := grpc.NewServer()
	documentv1.RegisterDocumentServiceServer(grpcServer, srv)

	conn, cleanup, err := dialer(grpcServer)
	require.NoError(t, err)
	defer cleanup()

	client := documentv1.NewDocumentServiceClient(conn)
	ctx := metadata.NewOutgoingContext(context.Background(), metadata.Pairs("x-correlation-id", "test-corr"))

	created, err := client.CreateDocument(ctx, &documentv1.CreateDocumentRequest{FileName: "doc", Repository: "owner/repo", Branch: "main", BaseBranch: "main", Path: "docs/doc.md", UploadBucket: "orig", UploadObjectKey: "docs/doc.docx", MarkdownBucket: "md", MarkdownObjectKey: "docs/doc.md", Owner: "owner"})
	require.NoError(t, err)
	require.Equal(t, documentv1.DocumentStatus_DOCUMENT_STATUS_UPLOADED, created.Status)
	require.Equal(t, "repo", created.Repository)
	require.Equal(t, "main", created.Branch)
	require.Equal(t, "owner", created.Owner)
	require.Equal(t, "docs/doc.md", created.Path)

	updated, err := client.UpdateDocumentStatus(ctx, &documentv1.UpdateDocumentStatusRequest{Id: created.Id, Status: documentv1.DocumentStatus_DOCUMENT_STATUS_STORED_ORIGINAL, Version: 2, MarkdownObjectKey: "md/key2"})
	require.NoError(t, err)
	require.Equal(t, documentv1.DocumentStatus_DOCUMENT_STATUS_STORED_ORIGINAL, updated.Status)

	listed, err := client.ListDocuments(ctx, &documentv1.ListDocumentsRequest{})
	require.NoError(t, err)
	require.Len(t, listed.Documents, 1)

	fetched, err := client.GetDocument(ctx, &documentv1.GetDocumentRequest{Id: created.Id})
	require.NoError(t, err)
	require.Equal(t, created.Id, fetched.Id)
	require.Equal(t, created.Repository, fetched.Repository)
	require.Equal(t, created.Path, fetched.Path)
}

type noopStorageClient struct{}

func (noopStorageClient) UploadOriginal(context.Context, *storagev1.UploadOriginalRequest, ...grpc.CallOption) (*storagev1.UploadObjectResponse, error) {
	return &storagev1.UploadObjectResponse{}, nil
}

func (noopStorageClient) UploadMarkdown(context.Context, *storagev1.UploadMarkdownRequest, ...grpc.CallOption) (*storagev1.UploadObjectResponse, error) {
	return &storagev1.UploadObjectResponse{}, nil
}

func (noopStorageClient) GetObject(_ context.Context, in *storagev1.GetObjectRequest, _ ...grpc.CallOption) (*storagev1.GetObjectResponse, error) {
	return &storagev1.GetObjectResponse{Bucket: in.GetBucket(), ObjectKey: in.GetObjectKey()}, nil
}

func (noopStorageClient) GenerateUploadURL(context.Context, *storagev1.GenerateUploadURLRequest, ...grpc.CallOption) (*storagev1.GenerateUploadURLResponse, error) {
	return &storagev1.GenerateUploadURLResponse{}, nil
}
