package document

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"go.temporal.io/sdk/client"

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
	repo                  Repository
	keyManager            storage.KeyManager
	temporal              WorkflowStarter
	taskQueue             string
	defaultOriginalBucket string
	defaultMarkdownBucket string
}

func New(repo Repository, keyManager storage.KeyManager, temporal WorkflowStarter, taskQueue string, activities *Activities, defaultOriginalBucket, defaultMarkdownBucket string) *Service {
	return &Service{
		repo:                  repo,
		keyManager:            keyManager,
		temporal:              temporal,
		taskQueue:             taskQueue,
		defaultOriginalBucket: defaultOriginalBucket,
		defaultMarkdownBucket: defaultMarkdownBucket,
	}
}

type CreateInput struct {
	ID                string
	FileName          string
	Repository        string
	Branch            string
	Path              string
	UploadBucket      string
	UploadObjectKey   string
	MarkdownBucket    string
	MarkdownObjectKey string
	OriginalContent   []byte
	FilenameExt       string
	PublishPath       string
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
	if strings.TrimSpace(in.FileName) == "" {
		return nil, errors.New("name is required")
	}
	if in.ID != "" {
		if _, err := uuid.Parse(in.ID); err != nil {
			return nil, errors.New("id is not a valid uuid")
		}
	}
	originalBucket := in.UploadBucket
	if originalBucket == "" {
		originalBucket = s.defaultOriginalBucket
	}
	markdownBucket := in.MarkdownBucket
	if markdownBucket == "" {
		markdownBucket = s.defaultMarkdownBucket
	}
	if originalBucket == "" || markdownBucket == "" {
		return nil, errors.New("buckets are required")
	}
	pair, err := s.keyManager.GenerateKeys(ctx)
	if err != nil {
		return nil, fmt.Errorf("generate keys: %w", err)
	}
	originalKey := in.UploadObjectKey
	if originalKey == "" {
		originalKey = pair.OriginalObjectKey
	}
	markdownKey := in.MarkdownObjectKey
	if markdownKey == "" {
		markdownKey = pair.MarkdownObjectKey
	}

	doc := &model.Document{
		ID:                in.ID,
		Name:              in.FileName,
		Status:            model.StatusUploaded,
		OriginalBucket:    originalBucket,
		OriginalObjectKey: originalKey,
		MarkdownBucket:    markdownBucket,
		MarkdownObjectKey: markdownKey,
	}
	version := &model.DocumentVersion{
		Version:           1,
		OriginalObjectKey: originalKey,
		MarkdownObjectKey: markdownKey,
	}
	log := &model.DocumentStatusLog{ToStatus: model.StatusUploaded}
	created, err := s.repo.CreateDocument(ctx, doc, version, log)
	if err != nil {
		return nil, err
	}
	if s.temporal != nil {
		opts := client.StartWorkflowOptions{
			ID:        created.ID,
			TaskQueue: s.taskQueue,
		}

		input := WorkflowInput{
			DocumentID:        created.ID,
			OriginalBucket:    created.OriginalBucket,
			MarkdownBucket:    created.MarkdownBucket,
			OriginalObjectKey: created.OriginalObjectKey,
			MarkdownObjectKey: created.MarkdownObjectKey,
			FilenameExt:       in.FilenameExt,
			PublishPath:       in.PublishPath,
			OriginalContent:   in.OriginalContent,
			Version:           version.Version,
		}

		if _, err := s.temporal.ExecuteWorkflow(ctx, opts, DocumentProcessingWorkflow, input); err != nil {
			return nil, fmt.Errorf("start workflow: %w", err)
		}
	}
	return created, nil
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
