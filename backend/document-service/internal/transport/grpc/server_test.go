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
	"backend-document-service/internal/domain"
	"backend-document-service/internal/grpcdial"
	usecase "backend-document-service/internal/usecase/document"
	documentv1 "backend-document-service/proto/backend/document/v1"

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
	documents map[string]domain.Document
	logs      map[string][]domain.DocumentStatus
	jobs      map[string]domain.ProcessingJob
}

func (m *memoryRepo) CreateDocument(ctx context.Context, doc *domain.Document, version *domain.DocumentVersion, log *domain.DocumentStatusLog) (*domain.Document, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.documents == nil {
		m.documents = make(map[string]domain.Document)
	}
	doc.ID = uuid.NewString()
	doc.Versions = []domain.DocumentVersion{*version}
	m.documents[doc.ID] = *doc
	return doc, nil
}

func (m *memoryRepo) GetDocument(ctx context.Context, id string) (*domain.Document, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	doc, ok := m.documents[id]
	if !ok {
		return nil, errors.New("not found")
	}
	return &doc, nil
}

func (m *memoryRepo) ListDocuments(ctx context.Context, limit, offset int, status *domain.DocumentStatus) ([]domain.Document, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []domain.Document
	for _, d := range m.documents {
		if status != nil && d.Status != *status {
			continue
		}
		out = append(out, d)
	}
	return out, nil
}

func (m *memoryRepo) CountStatusLogsWithReasonPrefix(ctx context.Context, id string, status domain.DocumentStatus, reasonPrefix string) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.logs[id]), nil
}

func (m *memoryRepo) UpdateStatus(ctx context.Context, id string, to domain.DocumentStatus, reason, errorReason string, version *domain.DocumentVersion) (*domain.Document, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	doc, ok := m.documents[id]
	if !ok {
		return nil, errors.New("not found")
	}
	if err := domain.ValidateDocumentStatusTransition(doc.Status, to); err != nil {
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
		m.logs = make(map[string][]domain.DocumentStatus)
	}
	m.logs[id] = append(m.logs[id], to)
	m.documents[id] = doc
	return &doc, nil
}

func (m *memoryRepo) UpdateWorkflowID(ctx context.Context, id, workflowID string) (*domain.Document, error) {
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

func (m *memoryRepo) CreateProcessingJob(ctx context.Context, job *domain.ProcessingJob, event *domain.JobEvent) (*domain.ProcessingJob, error) {
	if job.TaskID == "" {
		job.TaskID = uuid.NewString()
	}
	if m.jobs == nil {
		m.jobs = make(map[string]domain.ProcessingJob)
	}
	if job.Status == "" {
		job.Status = domain.JobStatusPending
	}
	m.jobs[job.TaskID] = *job
	return job, nil
}

func (m *memoryRepo) UpdateProcessingJob(ctx context.Context, taskID string, status domain.JobStatus, nextRunAt *time.Time, incrementAttempt bool, payload map[string]any, event *domain.JobEvent) (*domain.ProcessingJob, error) {
	job := domain.ProcessingJob{TaskID: taskID, Status: status}
	if nextRunAt != nil {
		job.NextRunAt = *nextRunAt
	}
	if payload != nil {
		job.Payload = payload
	}
	if m.jobs == nil {
		m.jobs = make(map[string]domain.ProcessingJob)
	}
	m.jobs[taskID] = job
	return &job, nil
}

func (m *memoryRepo) ActiveJob(ctx context.Context, documentID string) (*domain.ProcessingJob, error) {
	for _, job := range m.jobs {
		if job.DocumentID != documentID {
			continue
		}
		if job.Status == domain.JobStatusPending || job.Status == domain.JobStatusInProgress {
			copy := job
			return &copy, nil
		}
	}
	return nil, nil
}

func (m *memoryRepo) RequeueStaleProcessingJobs(context.Context, time.Time, int, time.Time, string) ([]domain.ProcessingJob, error) {
	return nil, nil
}

func (m *memoryRepo) LastNonFailedStatus(ctx context.Context, id string) (domain.DocumentStatus, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	doc, ok := m.documents[id]
	if !ok {
		return "", errors.New("not found")
	}
	history := m.logs[id]
	if len(history) == 0 {
		if doc.Status != domain.DocumentStatusFailed {
			return doc.Status, nil
		}
		return "", sql.ErrNoRows
	}
	for i := len(history) - 1; i >= 0; i-- {
		if history[i] != domain.DocumentStatusFailed {
			return history[i], nil
		}
	}
	return "", sql.ErrNoRows
}

func TestGRPCWorkflow(t *testing.T) {
	repo := &memoryRepo{}
	storage := &noopStorage{}
	defaults := usecase.Defaults{OriginalBucket: "orig", MarkdownBucket: "md", Owner: "owner", Repository: "repo", Branch: "main", BaseBranch: "main"}
	uc := usecase.New(repo, storage, defaults)
	srv := NewServer(uc)
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

type noopStorage struct{}

func (noopStorage) ValidateObject(context.Context, string, string) error { return nil }
