package persistence

import (
	"context"
	"database/sql"
	"time"

	"backend-document-service/internal/domain"
	"backend-document-service/internal/repository"
)

type SQLRepository struct {
	*repository.Repository
}

func NewSQLRepository(db *sql.DB) *SQLRepository {
	return &SQLRepository{Repository: repository.New(db)}
}

func (r *SQLRepository) CreateDocument(ctx context.Context, doc *domain.Document, version *domain.DocumentVersion, log *domain.DocumentStatusLog) (*domain.Document, error) {
	return r.Repository.CreateDocument(ctx, doc, version, log)
}

func (r *SQLRepository) GetDocument(ctx context.Context, id string) (*domain.Document, error) {
	return r.Repository.GetDocument(ctx, id)
}

func (r *SQLRepository) ListDocuments(ctx context.Context, limit, offset int, status *domain.Status) ([]domain.Document, error) {
	return r.Repository.ListDocuments(ctx, limit, offset, status)
}

func (r *SQLRepository) CountStatusLogsWithReasonPrefix(ctx context.Context, id string, status domain.Status, reasonPrefix string) (int, error) {
	return r.Repository.CountStatusLogsWithReasonPrefix(ctx, id, status, reasonPrefix)
}

func (r *SQLRepository) UpdateStatus(ctx context.Context, id string, to domain.Status, reason, errorReason string, version *domain.DocumentVersion) (*domain.Document, error) {
	return r.Repository.UpdateStatus(ctx, id, to, reason, errorReason, version)
}

func (r *SQLRepository) LastNonFailedStatus(ctx context.Context, id string) (domain.Status, error) {
	return r.Repository.LastNonFailedStatus(ctx, id)
}

func (r *SQLRepository) ActiveJob(ctx context.Context, documentID string) (*domain.ProcessingJob, error) {
	return r.Repository.ActiveJob(ctx, documentID)
}

func (r *SQLRepository) CreateProcessingJob(ctx context.Context, job *domain.ProcessingJob, event *domain.JobEvent) (*domain.ProcessingJob, error) {
	return r.Repository.CreateProcessingJob(ctx, job, event)
}

func (r *SQLRepository) UpdateProcessingJob(ctx context.Context, taskID string, status domain.JobStatus, nextRunAt *time.Time, incrementAttempt bool, payload map[string]any, event *domain.JobEvent) (*domain.ProcessingJob, error) {
	return r.Repository.UpdateProcessingJob(ctx, taskID, status, nextRunAt, incrementAttempt, payload, event)
}

func (r *SQLRepository) RequeueStaleProcessingJobs(ctx context.Context, staleBefore time.Time, limit int, nextRunAt time.Time, reason string) ([]domain.ProcessingJob, error) {
	return r.Repository.RequeueStaleProcessingJobs(ctx, staleBefore, limit, nextRunAt, reason)
}
