package document

import (
	"context"

	"backend-document-service/internal/domain"
)

// Repository описывает операции хранения, необходимые интеракторам документа.
type Repository interface {
	CreateDocument(ctx context.Context, doc *domain.Document, version *domain.DocumentVersion, log *domain.DocumentStatusLog) (*domain.Document, error)
	GetDocument(ctx context.Context, id string) (*domain.Document, error)
	ListDocuments(ctx context.Context, limit, offset int, status *domain.DocumentStatus) ([]domain.Document, error)
	UpdateStatus(ctx context.Context, id string, to domain.DocumentStatus, reason, errorReason string, version *domain.DocumentVersion) (*domain.Document, error)
	ActiveJob(ctx context.Context, documentID string) (*domain.ProcessingJob, error)
	CreateProcessingJob(ctx context.Context, job *domain.ProcessingJob, event *domain.JobEvent) (*domain.ProcessingJob, error)
}

// Storage описывает операции проверки доступности объектов в файловом хранилище.
type Storage interface {
	ValidateObject(ctx context.Context, bucket, objectKey string) error
}

// JobScheduler описывает операции для постановки следующего этапа обработки.
type JobScheduler interface {
	CreateProcessingJob(ctx context.Context, job *domain.ProcessingJob, event *domain.JobEvent) (*domain.ProcessingJob, error)
	ActiveJob(ctx context.Context, documentID string) (*domain.ProcessingJob, error)
}

// StatusUpdater обновляет статус документа.
type StatusUpdater interface {
	UpdateStatus(ctx context.Context, id string, to domain.DocumentStatus, reason, errorReason string, version *domain.DocumentVersion) (*domain.Document, error)
}
