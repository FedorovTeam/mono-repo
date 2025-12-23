package document

import (
	"context"
	"testing"
	"time"

	usecase "backend-document-service/internal/usecase/document"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"backend-document-service/internal/domain"
)

type confirmUploadMock struct {
	calls     int
	lastInput usecase.ConfirmUploadInput
}

func (c *confirmUploadMock) ConfirmUpload(ctx context.Context, in usecase.ConfirmUploadInput) (*domain.Document, error) {
	c.calls++
	c.lastInput = in
	return &domain.Document{ID: in.DocumentID}, nil
}

type repoStub struct {
	doc  *domain.Document
	docs []domain.Document
}

func (r *repoStub) CreateDocument(context.Context, *domain.Document, *domain.DocumentVersion, *domain.DocumentStatusLog) (*domain.Document, error) {
	return r.doc, nil
}

func (r *repoStub) GetDocument(context.Context, string) (*domain.Document, error) { return r.doc, nil }

func (r *repoStub) ListDocuments(context.Context, int, int, *domain.DocumentStatus) ([]domain.Document, error) {
	return r.docs, nil
}

func (r *repoStub) UpdateStatus(context.Context, string, domain.DocumentStatus, string, string, *domain.DocumentVersion) (*domain.Document, error) {
	return r.doc, nil
}

func (r *repoStub) UpdateDocumentOptions(context.Context, string, domain.ConversionStrategy, *bool) (*domain.Document, error) {
	return r.doc, nil
}

func (r *repoStub) ActiveJob(context.Context, string) (*domain.ProcessingJob, error) { return nil, nil }

func (r *repoStub) CreateProcessingJob(context.Context, *domain.ProcessingJob, *domain.JobEvent) (*domain.ProcessingJob, error) {
	return nil, nil
}

func (r *repoStub) CountStatusLogsWithReasonPrefix(context.Context, string, domain.DocumentStatus, string) (int, error) {
	return 0, nil
}

func (r *repoStub) AddJobEvent(context.Context, *domain.JobEvent) error { return nil }

func (r *repoStub) LastNonFailedStatus(context.Context, string) (domain.DocumentStatus, error) {
	return domain.DocumentStatus(""), nil
}

func (r *repoStub) UpdateProcessingJob(context.Context, string, domain.JobStatus, *time.Time, bool, map[string]any, *domain.JobEvent) (*domain.ProcessingJob, error) {
	return nil, nil
}

func (r *repoStub) RequeueStaleProcessingJobs(context.Context, time.Time, int, time.Time, string) ([]domain.ProcessingJob, error) {
	return nil, nil
}

func (r *repoStub) AcquirePendingJobs(context.Context, domain.JobStage, int) ([]domain.ProcessingJob, error) {
	return nil, nil
}

type staleJobRepo struct {
	repoStub
	jobs           []domain.ProcessingJob
	calledWith     time.Time
	lastReason     string
	lastNextRun    time.Time
	lastBatchLimit int
}

func (r *staleJobRepo) RequeueStaleProcessingJobs(ctx context.Context, staleBefore time.Time, limit int, nextRunAt time.Time, reason string) ([]domain.ProcessingJob, error) {
	r.calledWith = staleBefore
	r.lastReason = reason
	r.lastNextRun = nextRunAt
	r.lastBatchLimit = limit

	var restored []domain.ProcessingJob
	for i := range r.jobs {
		if r.jobs[i].Status != domain.JobStatusInProgress || r.jobs[i].UpdatedAt.After(staleBefore) || len(restored) >= limit {
			continue
		}
		r.jobs[i].Status = domain.JobStatusPending
		r.jobs[i].Attempts++
		r.jobs[i].NextRunAt = nextRunAt
		restored = append(restored, r.jobs[i])
	}
	return restored, nil
}

func TestUploadReconcilerConfirmsExistingObjects(t *testing.T) {
	fixed := time.Date(2024, 12, 1, 12, 0, 0, 0, time.UTC)
	doc := domain.Document{
		ID:                uuid.NewString(),
		OriginalBucket:    "bucket",
		OriginalObjectKey: "path/file.docx",
		Status:            domain.DocumentStatusUploaded,
		UpdatedAt:         fixed.Add(-2 * time.Minute),
	}
	repo := &repoStub{doc: &doc, docs: []domain.Document{doc}}
	confirm := &confirmUploadMock{}
	reconciler := NewUploadReconciler(repo, confirm, 10, time.Minute)
	reconciler.now = func() time.Time { return fixed }

	if err := reconciler.reconcile(context.Background()); err != nil {
		t.Fatalf("unexpected reconcile error: %v", err)
	}

	require.Equal(t, 1, confirm.calls)
	require.Equal(t, doc.ID, confirm.lastInput.DocumentID)
}

func TestUploadReconcilerSkipsRecentDocuments(t *testing.T) {
	fixed := time.Date(2024, 12, 1, 12, 0, 0, 0, time.UTC)
	doc := domain.Document{
		ID:                uuid.NewString(),
		OriginalBucket:    "bucket",
		OriginalObjectKey: "path/file.docx",
		Status:            domain.DocumentStatusUploaded,
		UpdatedAt:         fixed.Add(-30 * time.Second),
	}
	repo := &repoStub{doc: &doc, docs: []domain.Document{doc}}
	confirm := &confirmUploadMock{}
	reconciler := NewUploadReconciler(repo, confirm, 10, time.Minute)
	reconciler.now = func() time.Time { return fixed }

	if err := reconciler.reconcile(context.Background()); err != nil {
		t.Fatalf("unexpected reconcile error: %v", err)
	}

	require.Zero(t, confirm.calls)
}

func TestUploadReconcilerSkipsWhenNoObjectKey(t *testing.T) {
	fixed := time.Date(2024, 12, 1, 12, 0, 0, 0, time.UTC)
	doc := domain.Document{
		ID:                uuid.NewString(),
		OriginalBucket:    "",
		OriginalObjectKey: "",
		Status:            domain.DocumentStatusUploaded,
		UpdatedAt:         fixed.Add(-2 * time.Minute),
	}
	repo := &repoStub{doc: &doc, docs: []domain.Document{doc}}
	confirm := &confirmUploadMock{}
	reconciler := NewUploadReconciler(repo, confirm, 10, time.Minute)
	reconciler.now = func() time.Time { return fixed }

	if err := reconciler.reconcile(context.Background()); err != nil {
		t.Fatalf("unexpected reconcile error: %v", err)
	}

	require.Zero(t, confirm.calls)
}

func TestProcessingReconcilerReturnsStaleJobsToPending(t *testing.T) {
	fixed := time.Date(2024, 12, 1, 12, 0, 0, 0, time.UTC)
	repo := &staleJobRepo{jobs: []domain.ProcessingJob{{
		TaskID:     "task-1",
		DocumentID: "doc-1",
		Stage:      domain.JobStagePublishToGitHub,
		Status:     domain.JobStatusInProgress,
		Attempts:   1,
		UpdatedAt:  fixed.Add(-3 * time.Minute),
	}}}

	reconciler := NewProcessingReconciler(repo, 5, 30*time.Second, 15*time.Second)
	reconciler.now = func() time.Time { return fixed }

	if err := reconciler.reconcile(context.Background(), time.Minute); err != nil {
		t.Fatalf("unexpected reconcile error: %v", err)
	}

	require.Equal(t, domain.JobStatusPending, repo.jobs[0].Status)
	require.Equal(t, 2, repo.jobs[0].Attempts)
	require.Equal(t, fixed, repo.jobs[0].NextRunAt)
	require.Equal(t, fixed.Add(-(45 * time.Second)), repo.calledWith)
	require.Contains(t, repo.lastReason, "restored")
}
