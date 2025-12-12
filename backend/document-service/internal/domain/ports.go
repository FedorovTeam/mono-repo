package domain

import (
	"context"
	"time"
)

type DocumentRepository interface {
	CreateDocument(ctx context.Context, doc *Document, version *DocumentVersion, log *DocumentStatusLog) (*Document, error)
	GetDocument(ctx context.Context, id string) (*Document, error)
	ListDocuments(ctx context.Context, limit, offset int, status *Status) ([]Document, error)
	CountStatusLogsWithReasonPrefix(ctx context.Context, id string, status Status, reasonPrefix string) (int, error)
	UpdateStatus(ctx context.Context, id string, to Status, reason, errorReason string, version *DocumentVersion) (*Document, error)
	LastNonFailedStatus(ctx context.Context, id string) (Status, error)
	ActiveJob(ctx context.Context, documentID string) (*ProcessingJob, error)
	CreateProcessingJob(ctx context.Context, job *ProcessingJob, event *JobEvent) (*ProcessingJob, error)
	UpdateProcessingJob(ctx context.Context, taskID string, status JobStatus, nextRunAt *time.Time, incrementAttempt bool, payload map[string]any, event *JobEvent) (*ProcessingJob, error)
	RequeueStaleProcessingJobs(ctx context.Context, staleBefore time.Time, limit int, nextRunAt time.Time, reason string) ([]ProcessingJob, error)
}

type Storage interface {
	ValidateObject(ctx context.Context, bucket, objectKey string) error
}
