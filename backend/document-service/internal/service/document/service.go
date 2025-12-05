package document

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"backend-document-service/internal/model"
	"backend-document-service/internal/storage"
)

type Repository interface {
	CreateDocument(ctx context.Context, doc *model.Document, version *model.DocumentVersion, log *model.DocumentStatusLog) (*model.Document, error)
	GetDocument(ctx context.Context, id string) (*model.Document, error)
	ListDocuments(ctx context.Context, limit, offset int, status *model.Status) ([]model.Document, error)
	UpdateStatus(ctx context.Context, id string, to model.Status, reason string, version *model.DocumentVersion) (*model.Document, error)
}

type Service struct {
	repo       Repository
	keyManager storage.KeyManager
}

func New(repo Repository, keyManager storage.KeyManager) *Service {
	return &Service{repo: repo, keyManager: keyManager}
}

type CreateInput struct {
	Name           string
	OriginalBucket string
	MarkdownBucket string
}

type UpdateStatusInput struct {
	DocumentID        string
	Status            model.Status
	Reason            string
	MarkdownObjectKey string
	OriginalObjectKey string
	Version           int32
}

func (s *Service) CreateDocument(ctx context.Context, in CreateInput) (*model.Document, error) {
	if strings.TrimSpace(in.Name) == "" {
		return nil, errors.New("name is required")
	}
	if in.OriginalBucket == "" || in.MarkdownBucket == "" {
		return nil, errors.New("buckets are required")
	}
	pair, err := s.keyManager.GenerateKeys(ctx)
	if err != nil {
		return nil, fmt.Errorf("generate keys: %w", err)
	}

	doc := &model.Document{
		Name:              in.Name,
		Status:            model.StatusUploaded,
		OriginalBucket:    in.OriginalBucket,
		OriginalObjectKey: pair.OriginalObjectKey,
		MarkdownBucket:    in.MarkdownBucket,
		MarkdownObjectKey: pair.MarkdownObjectKey,
	}
	version := &model.DocumentVersion{
		Version:           1,
		OriginalObjectKey: pair.OriginalObjectKey,
		MarkdownObjectKey: pair.MarkdownObjectKey,
	}
	log := &model.DocumentStatusLog{ToStatus: model.StatusUploaded}
	return s.repo.CreateDocument(ctx, doc, version, log)
}

func (s *Service) GetDocument(ctx context.Context, id string) (*model.Document, error) {
	if _, err := uuid.Parse(id); err != nil {
		return nil, errors.New("invalid id")
	}
	return s.repo.GetDocument(ctx, id)
}

func (s *Service) ListDocuments(ctx context.Context, limit, offset int, status *model.Status) ([]model.Document, error) {
	if status != nil {
		if err := model.ValidateStatus(*status); err != nil {
			return nil, err
		}
	}
	return s.repo.ListDocuments(ctx, limit, offset, status)
}

func (s *Service) UpdateStatus(ctx context.Context, in UpdateStatusInput) (*model.Document, error) {
	if _, err := uuid.Parse(in.DocumentID); err != nil {
		return nil, errors.New("invalid id")
	}
	if err := model.ValidateStatus(in.Status); err != nil {
		return nil, err
	}

	var version *model.DocumentVersion
	if in.MarkdownObjectKey != "" || in.OriginalObjectKey != "" || in.Version > 0 {
		version = &model.DocumentVersion{
			Version:           in.Version,
			OriginalObjectKey: in.OriginalObjectKey,
			MarkdownObjectKey: in.MarkdownObjectKey,
		}
		if version.Version == 0 {
			version.Version = 1
		}
	}
	return s.repo.UpdateStatus(ctx, in.DocumentID, in.Status, in.Reason, version)
}
