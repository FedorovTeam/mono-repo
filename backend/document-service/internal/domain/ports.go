package domain

import (
	"context"
	"time"
)

// DocumentRepository определяет операции с документами и их статусами.
type DocumentRepository interface {
	CreateDocument(ctx context.Context, doc *Document, version *DocumentVersion, log *DocumentStatusLog) (*Document, error)
	GetDocument(ctx context.Context, id string) (*Document, error)
	UpdateStatus(ctx context.Context, id string, to DocumentStatus, reason, errorReason string, version *DocumentVersion) (*Document, error)
	UpdateWorkflowID(ctx context.Context, id, workflowID string) (*Document, error)
	LastNonFailedStatus(ctx context.Context, id string) (DocumentStatus, error)
}

// JobRepository управляет processing-задачами и их состояниями.
type JobRepository interface {
	ActiveJob(ctx context.Context, documentID string) (*ProcessingJob, error)
	ListFinishedJobs(ctx context.Context, limit int, postpone time.Duration) ([]ProcessingJob, error)
	AcquirePendingJobs(ctx context.Context, stage JobStage, limit int) ([]ProcessingJob, error)
	RequeueStaleProcessingJobs(ctx context.Context, staleBefore time.Time, limit int, nextRunAt time.Time, reason string) ([]ProcessingJob, error)
	CreateProcessingJob(ctx context.Context, job *ProcessingJob, event *JobEvent) (*ProcessingJob, error)
	UpdateProcessingJob(ctx context.Context, taskID string, status JobStatus, nextRunAt *time.Time, incrementAttempt bool, payload map[string]any, event *JobEvent) (*ProcessingJob, error)
}

// EventLogRepository сохраняет события обработки.
type EventLogRepository interface {
	AddJobEvent(ctx context.Context, event *JobEvent) error
}
