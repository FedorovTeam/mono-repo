package usecase

import (
	"backend-gateway-service/pkg/ctxutils"
	"context"

	"backend-gateway-service/internal/domain"
)

// DocumentProvider описывает сценарии получения документов.
type DocumentProvider interface {
	GetDocument(ctx context.Context, id string) (*domain.Document, error)
	ListDocuments(ctx context.Context, filter domain.DocumentFilter, pagination domain.Pagination) (*domain.DocumentList, error)
}

// DocumentService реализует сценарии получения документов.
type DocumentService struct {
	documents domain.DocumentClient
}

// NewDocumentService создаёт DocumentService.
func NewDocumentService(docClient domain.DocumentClient) *DocumentService {
	return &DocumentService{documents: docClient}
}

// GetDocument возвращает документ по идентификатору.
func (s *DocumentService) GetDocument(ctx context.Context, id string) (*domain.Document, error) {
	ctx, _ = ctxutils.EnsureCorrelationID(ctx)
	return s.documents.GetDocument(ctx, id)
}

// ListDocuments возвращает список документов с учётом фильтров.
func (s *DocumentService) ListDocuments(ctx context.Context, filter domain.DocumentFilter, pagination domain.Pagination) (*domain.DocumentList, error) {
	ctx, _ = ctxutils.EnsureCorrelationID(ctx)
	return s.documents.ListDocuments(ctx, filter, pagination)
}
