package document

import (
	"context"

	"backend-document-service/internal/domain"
)

// DocumentService описывает операции, доступные для внешних интерфейсов.
type DocumentService interface {
	CreateDocument(ctx context.Context, in CreateDocumentInput) (*domain.Document, error)
	ConfirmUpload(ctx context.Context, in ConfirmUploadInput) (*domain.Document, error)
	UpdateStatus(ctx context.Context, in UpdateStatusInput) (*domain.Document, error)
	GetDocument(ctx context.Context, id string) (*domain.Document, error)
	ListDocuments(ctx context.Context, limit, offset int, status *domain.DocumentStatus) (*domain.DocumentList, error)
}

var _ DocumentService = (*UseCase)(nil)
