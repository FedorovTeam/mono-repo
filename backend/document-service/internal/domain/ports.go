package domain

import (
	"context"
	"time"
)

// DocumentRepository описывает операции работы с документами и их статусами.
type DocumentRepository interface {
	CreateDocument(ctx context.Context, doc *Document, version *DocumentVersion, log *DocumentStatusLog) (*Document, error)
	GetDocument(ctx context.Context, id string) (*Document, error)
	ListDocuments(ctx context.Context, limit, offset int, status *DocumentStatus) ([]Document, error)
	ListStatusLogs(ctx context.Context, documentID string) ([]DocumentStatusLog, error)
	UpdateStatus(ctx context.Context, id string, to DocumentStatus, reason, errorReason string, version *DocumentVersion) (*Document, error)
	UpdateDocumentOptions(ctx context.Context, id string, strategy ConversionStrategy, extractTables *bool) (*Document, error)
	LastNonFailedStatus(ctx context.Context, id string) (DocumentStatus, error)
	CountStatusLogsWithReasonPrefix(ctx context.Context, id string, status DocumentStatus, reasonPrefix string) (int, error)
}

// JobRepository описывает работу с задачами фоновой обработки документов.
type JobRepository interface {
	CreateProcessingJob(ctx context.Context, job *ProcessingJob, event *JobEvent) (*ProcessingJob, error)
	ActiveJob(ctx context.Context, documentID string) (*ProcessingJob, error)
	AcquirePendingJobs(ctx context.Context, stage JobStage, limit int) ([]ProcessingJob, error)
	RequeueStaleProcessingJobs(ctx context.Context, staleBefore time.Time, limit int, nextRunAt time.Time, reason string) ([]ProcessingJob, error)
	UpdateProcessingJob(ctx context.Context, taskID string, status JobStatus, nextRunAt *time.Time, incrementAttempt bool, payload map[string]any, event *JobEvent) (*ProcessingJob, error)
}

// EventLogRepository описывает запись событий обработки.
type EventLogRepository interface {
	AddJobEvent(ctx context.Context, event *JobEvent) error
}
