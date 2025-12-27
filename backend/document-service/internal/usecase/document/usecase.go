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
	router        *domain.StageRouter
}

func New(repo Repository, storage Storage, defaults Defaults, router *domain.StageRouter) *UseCase {
	if router == nil {
		router = domain.NewStageRouter(domain.RouterRules{})
	}
	return &UseCase{
		create:        NewCreateDocument(repo, defaults, router),
		confirmUpload: NewConfirmUpload(repo, storage, router),
		updateStatus:  NewUpdateStatus(repo, router),
		repo:          repo,
		router:        router,
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

func (uc *UseCase) ListDocuments(ctx context.Context, limit, offset int, status *domain.DocumentStatus, repository *string) (*domain.DocumentList, error) {
	return uc.repo.ListDocuments(ctx, limit, offset, status, repository)
}
