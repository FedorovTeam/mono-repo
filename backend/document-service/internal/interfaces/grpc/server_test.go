package grpc

import (
	"context"
	"testing"

	"backend-document-service/internal/domain"
	document "backend-document-service/internal/usecase/document"
	documentv1 "backend-document-service/proto/backend/document/v1"

	"github.com/stretchr/testify/require"
)

type listDocumentsServiceStub struct {
	document.DocumentService
	receivedRepository *string
}

func (s *listDocumentsServiceStub) CreateDocument(context.Context, document.CreateDocumentInput) (*domain.Document, error) {
	return nil, nil
}

func (s *listDocumentsServiceStub) ConfirmUpload(context.Context, document.ConfirmUploadInput) (*domain.Document, error) {
	return nil, nil
}

func (s *listDocumentsServiceStub) UpdateStatus(context.Context, document.UpdateStatusInput) (*domain.Document, error) {
	return nil, nil
}

func (s *listDocumentsServiceStub) GetDocument(context.Context, string) (*domain.Document, error) {
	return nil, nil
}

func (s *listDocumentsServiceStub) ListDocuments(_ context.Context, limit, offset int, status *domain.DocumentStatus, repository *string) (*domain.DocumentList, error) {
	s.receivedRepository = repository
	return &domain.DocumentList{TotalCount: int64(limit + offset)}, nil
}

func TestListDocumentsPassesRepositoryFilter(t *testing.T) {
	svc := &listDocumentsServiceStub{}
	server := NewServer(svc)

	repo := "example/repo"
	_, err := server.ListDocuments(context.Background(), &documentv1.ListDocumentsRequest{Repository: repo})
	require.NoError(t, err)
	require.NotNil(t, svc.receivedRepository)
	require.Equal(t, repo, *svc.receivedRepository)
}
