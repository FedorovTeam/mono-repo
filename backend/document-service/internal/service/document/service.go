package document

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
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
	UpdateStatus(ctx context.Context, id string, to model.Status, reason, errorReason string, version *model.DocumentVersion) (*model.Document, error)
	UpdateWorkflowID(ctx context.Context, id, workflowID string) (*model.Document, error)
}

type Service struct {
	repo                  Repository
	keyManager            storage.KeyManager
	temporal              WorkflowStarter
	taskQueue             string
	defaultOriginalBucket string
	defaultMarkdownBucket string
	defaultOwner          string
	defaultRepository     string
	defaultBranch         string
	defaultBaseBranch     string
}

func New(repo Repository, keyManager storage.KeyManager, temporal WorkflowStarter, taskQueue string, _ *Activities, defaultOriginalBucket, defaultMarkdownBucket, defaultOwner, defaultRepository, defaultBranch, defaultBaseBranch string) *Service {
	return &Service{
		repo:                  repo,
		keyManager:            keyManager,
		temporal:              temporal,
		taskQueue:             taskQueue,
		defaultOriginalBucket: defaultOriginalBucket,
		defaultMarkdownBucket: defaultMarkdownBucket,
		defaultOwner:          defaultOwner,
		defaultRepository:     defaultRepository,
		defaultBranch:         defaultBranch,
		defaultBaseBranch:     defaultBaseBranch,
	}
}

type CreateInput struct {
	ID                string
	FileName          string
	Owner             string
	Repository        string
	Branch            string
	BaseBranch        string
	Path              string
	UploadBucket      string
	UploadObjectKey   string
	MarkdownBucket    string
	MarkdownObjectKey string
	OriginalContent   []byte
	FilenameExt       string
	PublishPath       string
	CreatePR          bool
}

type ConfirmUploadInput struct {
	DocumentID string
	Bucket     string
	ObjectKey  string
}

type UpdateStatusInput struct {
	DocumentID        string
	Status            model.Status
	Reason            string
	ErrorReason       string
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
	} else {
		in.ID = uuid.NewString()
	}
	owner := strings.TrimSpace(in.Owner)
	if owner == "" {
		owner = s.defaultOwner
	}
	repository := strings.TrimSpace(in.Repository)
	if repository == "" {
		repository = s.defaultRepository
	}
	branch := strings.TrimSpace(in.Branch)
	if branch == "" {
		branch = s.defaultBranch
	}
	baseBranch := strings.TrimSpace(in.BaseBranch)
	if baseBranch == "" {
		baseBranch = s.defaultBaseBranch
	}
	if baseBranch == "" {
		baseBranch = branch
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
	path := strings.TrimSpace(in.Path)
	if path == "" {
		path = in.FileName
	}

	publishPath := strings.TrimSpace(in.PublishPath)
	if publishPath == "" {
		publishPath = path
	}

	pair, err := s.keyManager.GenerateKeys(ctx, storage.KeyGenerationData{
		DocumentID:  in.ID,
		FileName:    in.FileName,
		Path:        path,
		PublishPath: publishPath,
	})
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
		Owner:             owner,
		Repository:        repository,
		Branch:            branch,
		BaseBranch:        baseBranch,
		Path:              path,
		WorkflowID:        in.ID,
		Status:            model.StatusUploaded,
		OriginalBucket:    originalBucket,
		OriginalObjectKey: originalKey,
		MarkdownBucket:    markdownBucket,
		MarkdownObjectKey: markdownKey,
		CreatePR:          in.CreatePR,
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
	return created, nil
}

func (s *Service) ConfirmUpload(ctx context.Context, in ConfirmUploadInput) (*model.Document, error) {
	if _, err := uuid.Parse(in.DocumentID); err != nil {
		return nil, errors.New("invalid id")
	}
	if strings.TrimSpace(in.ObjectKey) == "" {
		return nil, errors.New("object_key is required")
	}
	if strings.TrimSpace(in.Bucket) == "" {
		return nil, errors.New("bucket is required")
	}

	doc, err := s.repo.UpdateStatus(ctx, in.DocumentID, model.StatusStoredOriginal, "", "", &model.DocumentVersion{Version: 1, OriginalObjectKey: in.ObjectKey})
	if err != nil {
		return nil, err
	}
	if doc.OriginalBucket != "" && doc.OriginalBucket != in.Bucket {
		return nil, fmt.Errorf("bucket mismatch: expected %s, got %s", doc.OriginalBucket, in.Bucket)
	}
	if s.temporal == nil {
		return doc, nil
	}

	opts := client.StartWorkflowOptions{
		ID:        doc.ID,
		TaskQueue: s.taskQueue,
	}

	publishPath := doc.Path
	if publishPath == "" {
		publishPath = doc.Name
	}
	version := int32(1)
	if len(doc.Versions) > 0 {
		version = doc.Versions[len(doc.Versions)-1].Version
	}

	input := WorkflowInput{
		DocumentID:        doc.ID,
		DocumentName:      doc.Name,
		Owner:             doc.Owner,
		Repository:        doc.Repository,
		Branch:            doc.Branch,
		BaseBranch:        doc.BaseBranch,
		OriginalBucket:    doc.OriginalBucket,
		MarkdownBucket:    doc.MarkdownBucket,
		OriginalObjectKey: doc.OriginalObjectKey,
		MarkdownObjectKey: doc.MarkdownObjectKey,
		FilenameExt:       strings.TrimPrefix(filepath.Ext(in.ObjectKey), "."),
		PublishPath:       publishPath,
		Path:              doc.Path,
		Version:           version,
		CreatePR:          doc.CreatePR,
	}

	run, err := s.temporal.ExecuteWorkflow(ctx, opts, DocumentProcessingWorkflow, input)
	if err != nil {
		_, _ = s.repo.UpdateStatus(ctx, doc.ID, model.StatusFailed, fmt.Sprintf("start workflow: %v", err), fmt.Sprintf("start workflow: %v", err), nil)
		return nil, fmt.Errorf("start workflow: %w", err)
	}

	return s.repo.UpdateWorkflowID(ctx, doc.ID, run.GetID())
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

	if in.ErrorReason == "" && in.Status == model.StatusFailed {
		in.ErrorReason = in.Reason
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
	return s.repo.UpdateStatus(ctx, in.DocumentID, in.Status, in.Reason, in.ErrorReason, version)
}
