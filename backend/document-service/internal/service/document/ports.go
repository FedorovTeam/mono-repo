package document

import (
	"context"
	"time"

	"backend-document-service/internal/domain"
	usecase "backend-document-service/internal/usecase/document"
)

// Repository объединяет операции, необходимые воркерам и приложениям для управления документами.
type Repository interface {
	usecase.Repository
	CountStatusLogsWithReasonPrefix(ctx context.Context, id string, status domain.DocumentStatus, reasonPrefix string) (int, error)
	LastNonFailedStatus(ctx context.Context, id string) (domain.DocumentStatus, error)
	UpdateProcessingJob(ctx context.Context, taskID string, status domain.JobStatus, nextRunAt *time.Time, incrementAttempt bool, payload map[string]any, event *domain.JobEvent) (*domain.ProcessingJob, error)
	RequeueStaleProcessingJobs(ctx context.Context, staleBefore time.Time, limit int, nextRunAt time.Time, reason string) ([]domain.ProcessingJob, error)
	AcquirePendingJobs(ctx context.Context, stage domain.JobStage, limit int) ([]domain.ProcessingJob, error)
}
