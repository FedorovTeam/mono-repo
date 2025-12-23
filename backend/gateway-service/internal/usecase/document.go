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
	doc, err := s.documents.GetDocument(ctx, id)
	if err != nil {
		return nil, err
	}
	if err := s.attachStatusHistory(ctx, doc); err != nil {
		return nil, err
	}
	return doc, nil
}

// ListDocuments возвращает список документов с учётом фильтров.
func (s *DocumentService) ListDocuments(ctx context.Context, filter domain.DocumentFilter, pagination domain.Pagination) (*domain.DocumentList, error) {
	ctx, _ = ctxutils.EnsureCorrelationID(ctx)
	list, err := s.documents.ListDocuments(ctx, filter, pagination)
	if err != nil {
		return nil, err
	}
	for i := range list.Documents {
		if err := s.attachStatusHistory(ctx, &list.Documents[i]); err != nil {
			return nil, err
		}
	}
	return list, nil
}

func (s *DocumentService) attachStatusHistory(ctx context.Context, doc *domain.Document) error {
	history, err := s.documents.ListDocumentStatusLogs(ctx, doc.ID)
	if err != nil {
		return err
	}
	doc.StatusHistory = history
	return nil
}
