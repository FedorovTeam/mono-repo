package document

import (
	"context"

	"backend-document-service/internal/domain"
)

// UseCase агрегирует интеракторы документа.
type UseCase struct {
	create        *CreateDocument
	confirmUpload *ConfirmUpload
	updateStatus  *UpdateStatus
	repo          Repository
}

func New(repo Repository, storage Storage, defaults Defaults) *UseCase {
	return &UseCase{
		create:        NewCreateDocument(repo, defaults),
		confirmUpload: NewConfirmUpload(repo, storage),
		updateStatus:  NewUpdateStatus(repo),
		repo:          repo,
	}
}

func (uc *UseCase) CreateDocument(ctx context.Context, in CreateDocumentInput) (*domain.Document, error) {
	return uc.create.Execute(ctx, in)
}

func (uc *UseCase) ConfirmUpload(ctx context.Context, in ConfirmUploadInput) (*domain.Document, error) {
	return uc.confirmUpload.Execute(ctx, in)
}

func (uc *UseCase) UpdateStatus(ctx context.Context, in UpdateStatusInput) (*domain.Document, error) {
	return uc.updateStatus.Execute(ctx, in)
}

func (uc *UseCase) GetDocument(ctx context.Context, id string) (*domain.Document, error) {
	return uc.repo.GetDocument(ctx, id)
}

func (uc *UseCase) ListDocuments(ctx context.Context, limit, offset int, status *domain.DocumentStatus) ([]domain.Document, error) {
	return uc.repo.ListDocuments(ctx, limit, offset, status)
}
