package document

import (
        "context"

        "backend-document-service/internal/domain"
)

// Repository описывает операции хранения, необходимые интеракторам документа.
type Repository interface {
        domain.DocumentRepository
        domain.JobRepository
}

// Storage описывает операции проверки доступности объектов в файловом хранилище.
type Storage interface {
	ValidateObject(ctx context.Context, bucket, objectKey string) error
}

// JobScheduler описывает операции для постановки следующего этапа обработки.
type JobScheduler interface {
        domain.JobRepository
}

// StatusUpdater обновляет статус документа.
type StatusUpdater interface {
        UpdateStatus(ctx context.Context, id string, to domain.DocumentStatus, reason, errorReason string, version *domain.DocumentVersion) (*domain.Document, error)
}
