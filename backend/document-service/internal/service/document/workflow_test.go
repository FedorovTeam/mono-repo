package document

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"

	"backend-document-service/internal/domain"
	githubpublisherv1 "backend-document-service/proto/backend/githubpublisher/v1"
	storagev1 "backend-document-service/proto/backend/storage/v1"
	transformerv1 "backend-document-service/proto/backend/transformer/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type fakeRepo struct {
	lastStatus  domain.DocumentStatus
	lastVersion *domain.DocumentVersion
	lastError   string
	doc         *domain.Document
	jobs        map[string]domain.ProcessingJob
}

func (f *fakeRepo) CreateDocument(ctx context.Context, doc *domain.Document, version *domain.DocumentVersion, log *domain.DocumentStatusLog) (*domain.Document, error) {
	return nil, nil
}

func (f *fakeRepo) GetDocument(ctx context.Context, id string) (*domain.Document, error) {
	if f.doc != nil && f.doc.ID == id {
		return f.doc, nil
	}
	return nil, fmt.Errorf("document %s not found", id)
}

func (f *fakeRepo) ListDocuments(ctx context.Context, limit, offset int, status *domain.DocumentStatus) ([]domain.Document, error) {
	return nil, nil
}

func (f *fakeRepo) CountStatusLogsWithReasonPrefix(ctx context.Context, id string, status domain.DocumentStatus, reasonPrefix string) (int, error) {
	return 0, nil
}

func (f *fakeRepo) LastNonFailedStatus(ctx context.Context, id string) (domain.DocumentStatus, error) {
	if f.doc != nil {
		return f.doc.Status, nil
	}
	return domain.DocumentStatus(""), nil
}

func (f *fakeRepo) UpdateStatus(ctx context.Context, id string, to domain.DocumentStatus, reason, errorReason string, version *domain.DocumentVersion) (*domain.Document, error) {
	f.lastStatus = to
	f.lastVersion = version
	f.lastError = errorReason
	return &domain.Document{ID: id, Status: to}, nil
}

func (f *fakeRepo) CreateProcessingJob(ctx context.Context, job *domain.ProcessingJob, event *domain.JobEvent) (*domain.ProcessingJob, error) {
	if job.TaskID == "" {
		job.TaskID = uuid.NewString()
	}
	if f.jobs == nil {
		f.jobs = make(map[string]domain.ProcessingJob)
	}
	if job.Status == "" {
		job.Status = domain.JobStatusPending
	}
	f.jobs[job.TaskID] = *job
	return job, nil
}

func (f *fakeRepo) UpdateProcessingJob(ctx context.Context, taskID string, status domain.JobStatus, nextRunAt *time.Time, incrementAttempt bool, payload map[string]any, event *domain.JobEvent) (*domain.ProcessingJob, error) {
	job := domain.ProcessingJob{TaskID: taskID, Status: status}
	if payload != nil {
		job.Payload = payload
	}
	if nextRunAt != nil {
		job.NextRunAt = *nextRunAt
	}
	if f.jobs == nil {
		f.jobs = make(map[string]domain.ProcessingJob)
	}
	f.jobs[taskID] = job
	return &job, nil
}

func (f *fakeRepo) ActiveJob(ctx context.Context, documentID string) (*domain.ProcessingJob, error) {
	for _, job := range f.jobs {
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

func (f *fakeRepo) AddJobEvent(context.Context, *domain.JobEvent) error { return nil }

func (f *fakeRepo) RequeueStaleProcessingJobs(context.Context, time.Time, int, time.Time, string) ([]domain.ProcessingJob, error) {
	return nil, nil
}

func (f *fakeRepo) AcquirePendingJobs(context.Context, domain.JobStage, int) ([]domain.ProcessingJob, error) {
	return nil, nil
}

type fakeTransformerClient struct {
	lastRequest *transformerv1.ConvertDocumentToMarkdownRequest
	resp        *transformerv1.ConvertDocumentToMarkdownResponse
	err         error
}

func (f *fakeTransformerClient) ConvertDocumentToMarkdown(ctx context.Context, in *transformerv1.ConvertDocumentToMarkdownRequest, opts ...grpc.CallOption) (*transformerv1.ConvertDocumentToMarkdownResponse, error) {
	f.lastRequest = in
	return f.resp, f.err
}

func (*fakeTransformerClient) ConvertDocxToMarkdown(ctx context.Context, in *transformerv1.ConvertDocxToMarkdownRequest, opts ...grpc.CallOption) (*transformerv1.ConvertDocxToMarkdownResponse, error) {
	return nil, status.Error(codes.Unimplemented, "not implemented")
}

type fakeStorageClient struct {
	markdownReq *storagev1.UploadMarkdownRequest
	resp        *storagev1.UploadObjectResponse
	err         error
	getReq      *storagev1.GetObjectRequest
}

func (f *fakeStorageClient) UploadOriginal(ctx context.Context, in *storagev1.UploadOriginalRequest, opts ...grpc.CallOption) (*storagev1.UploadObjectResponse, error) {
	return nil, nil
}

func (f *fakeStorageClient) UploadMarkdown(ctx context.Context, in *storagev1.UploadMarkdownRequest, opts ...grpc.CallOption) (*storagev1.UploadObjectResponse, error) {
	f.markdownReq = in
	return f.resp, f.err
}

func (f *fakeStorageClient) GetObject(ctx context.Context, in *storagev1.GetObjectRequest, opts ...grpc.CallOption) (*storagev1.GetObjectResponse, error) {
	f.getReq = in
	return &storagev1.GetObjectResponse{Bucket: in.GetBucket(), ObjectKey: in.GetObjectKey()}, nil
}

func (f *fakeStorageClient) GenerateUploadURL(ctx context.Context, in *storagev1.GenerateUploadURLRequest, opts ...grpc.CallOption) (*storagev1.GenerateUploadURLResponse, error) {
	return nil, nil
}

type fakePublisher struct{}

func (fakePublisher) PublishMarkdown(ctx context.Context, in *githubpublisherv1.PublishMarkdownRequest, opts ...grpc.CallOption) (*githubpublisherv1.PublishMarkdownResponse, error) {
	return &githubpublisherv1.PublishMarkdownResponse{Status: githubpublisherv1.PublishResultStatus_PUBLISH_RESULT_STATUS_SUCCESS, CommitSha: "abc"}, nil
}

type trackingPublisher struct {
	called bool
}

func (p *trackingPublisher) PublishMarkdown(ctx context.Context, in *githubpublisherv1.PublishMarkdownRequest, opts ...grpc.CallOption) (*githubpublisherv1.PublishMarkdownResponse, error) {
	p.called = true
	return &githubpublisherv1.PublishMarkdownResponse{Status: githubpublisherv1.PublishResultStatus_PUBLISH_RESULT_STATUS_SUCCESS, CommitSha: "abc"}, nil
}

func TestConvertToMarkdownUsesUpdatedStub(t *testing.T) {
	repo := &fakeRepo{doc: &domain.Document{ID: "doc-1", Status: domain.DocumentStatusStoredOriginal, Versions: []domain.DocumentVersion{{Version: 2, MarkdownObjectKey: "path/doc-1.md"}}, OriginalBucket: "orig-bucket", OriginalObjectKey: "path/doc-1.docx", MarkdownBucket: "md-bucket", MarkdownObjectKey: "path/doc-1.md"}}
	transformer := &fakeTransformerClient{resp: &transformerv1.ConvertDocumentToMarkdownResponse{QueueId: "queue-1", Status: string(domain.DocumentStatusConvertedToMarkdown), MarkdownObjectKey: "md-key"}}

	activities := NewActivities(repo, transformer, fakePublisher{})

	result, err := activities.ConvertToMarkdown(context.Background(), convertInput{
		DocumentID:        "doc-1",
		OriginalBucket:    "orig-bucket",
		MarkdownBucket:    "md-bucket",
		OriginalObjectKey: "path/doc-1.docx",
		MarkdownObjectKey: "path/doc-1.md",
		Version:           3,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if transformer.lastRequest == nil {
		t.Fatal("transformer request was not sent")
	}

	if transformer.lastRequest.GetDocumentId() != "doc-1" || transformer.lastRequest.GetOriginalBucket() != "orig-bucket" {
		t.Fatalf("unexpected transformer request: %+v", transformer.lastRequest)
	}

        if transformer.lastRequest.GetMarkdownObjectKey() != "path/doc-1.md" || transformer.lastRequest.GetWaitForResult() {
                t.Fatalf("unexpected transformer request: %+v", transformer.lastRequest)
        }

	if result.QueueID != "queue-1" {
		t.Fatalf("unexpected queue id: %s", result.QueueID)
	}

	if result.Status != string(domain.DocumentStatusConvertedToMarkdown) {
		t.Fatalf("unexpected status: %s", result.Status)
	}

	if err := activities.MarkConverted(context.Background(), repo.doc, result.MarkdownKey, result.QueueID, result.Version); err != nil {
		t.Fatalf("mark converted failed: %v", err)
	}

	if repo.lastStatus != domain.DocumentStatusConvertedToMarkdown {
		t.Fatalf("status was not updated, got %v", repo.lastStatus)
	}

	if repo.lastVersion == nil || repo.lastVersion.Version != 3 || repo.lastVersion.MarkdownObjectKey != "md-key" {
		t.Fatalf("version was not saved correctly: %+v", repo.lastVersion)
	}

	if repo.lastVersion.MarkdownQueueID != "queue-1" {
		t.Fatalf("queue id was not saved correctly: %s", repo.lastVersion.MarkdownQueueID)
	}
}

func TestAwaitOriginalLoadsDocumentAndUpdatesStatus(t *testing.T) {
	doc := &domain.Document{
		ID:                "doc-1",
		Name:              "Document title",
		Owner:             "owner",
		Repository:        "repo",
		Branch:            "main",
		BaseBranch:        "main",
		Path:              "docs/doc-1.md",
		Status:            domain.DocumentStatusUploaded,
		OriginalBucket:    "orig",
		OriginalObjectKey: "orig/doc-1.docx",
		MarkdownBucket:    "md",
		MarkdownObjectKey: "md/doc-1.md",
		Versions:          []domain.DocumentVersion{{Version: 1, OriginalObjectKey: "orig/doc-1.docx", MarkdownObjectKey: "md/doc-1.md"}},
	}
	repo := &fakeRepo{doc: doc}
	activities := NewActivities(repo, nil, nil)

	if err := activities.MarkStoredOriginal(context.Background(), doc.ID); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if repo.lastStatus != domain.DocumentStatusStoredOriginal {
		t.Fatalf("expected status StoredOriginal, got %v", repo.lastStatus)
	}
}

func TestPublishMarkdownContinuesWhenRepositoryAccessible(t *testing.T) {
	repo := &fakeRepo{doc: &domain.Document{ID: "doc-2", Owner: "owner", Repository: "repo", Branch: "main", BaseBranch: "main", Path: "doc.md", MarkdownBucket: "bucket", MarkdownObjectKey: "object", Status: domain.DocumentStatusConvertedToMarkdown}}
	publisher := &trackingPublisher{}

	activities := NewActivities(repo, nil, publisher)

	_, err := activities.PublishMarkdown(context.Background(), publishInput{
		DocumentID:        "doc-2",
		DocumentName:      "doc title",
		MarkdownBucket:    "bucket",
		MarkdownObjectKey: "object",
		Path:              "doc.md",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !publisher.called {
		t.Fatal("publisher was not called")
	}
	if repo.lastStatus != domain.DocumentStatusPublishedToGitHub {
		t.Fatalf("expected published status, got %v", repo.lastStatus)
	}
}
