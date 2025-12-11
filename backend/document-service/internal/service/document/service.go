package document

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"backend-document-service/internal/model"
	"backend-document-service/internal/storage"
)

type Repository interface {
	CreateDocument(ctx context.Context, doc *model.Document, version *model.DocumentVersion, log *model.DocumentStatusLog) (*model.Document, error)
	GetDocument(ctx context.Context, id string) (*model.Document, error)
	ListDocuments(ctx context.Context, limit, offset int, status *model.Status) ([]model.Document, error)
	CountStatusLogsWithReasonPrefix(ctx context.Context, id string, status model.Status, reasonPrefix string) (int, error)
	UpdateStatus(ctx context.Context, id string, to model.Status, reason, errorReason string, version *model.DocumentVersion) (*model.Document, error)
	LastNonFailedStatus(ctx context.Context, id string) (model.Status, error)
	CreateProcessingJob(ctx context.Context, job *model.ProcessingJob, event *model.JobEvent) (*model.ProcessingJob, error)
	UpdateProcessingJob(ctx context.Context, taskID string, status model.JobStatus, nextRunAt *time.Time, incrementAttempt bool, payload map[string]any, event *model.JobEvent) (*model.ProcessingJob, error)
}

type Service struct {
	repo                  Repository
	keyManager            storage.KeyManager
	defaultOriginalBucket string
	defaultMarkdownBucket string
	defaultOwner          string
	defaultRepository     string
	defaultBranch         string
	defaultBaseBranch     string
}

func New(repo Repository, keyManager storage.KeyManager, _ *Activities, defaultOriginalBucket, defaultMarkdownBucket, defaultOwner, defaultRepository, defaultBranch, defaultBaseBranch string) *Service {
	return &Service{
		repo:                  repo,
		keyManager:            keyManager,
		defaultOriginalBucket: defaultOriginalBucket,
		defaultMarkdownBucket: defaultMarkdownBucket,
		defaultOwner:          defaultOwner,
		defaultRepository:     defaultRepository,
		defaultBranch:         defaultBranch,
		defaultBaseBranch:     defaultBaseBranch,
	}
}

type CreateInput struct {
	ID                 string
	FileName           string
	Owner              string
	Repository         string
	Branch             string
	BaseBranch         string
	Path               string
	UploadBucket       string
	UploadObjectKey    string
	MarkdownBucket     string
	MarkdownObjectKey  string
	OriginalContent    []byte
	FilenameExt        string
	PublishPath        string
	EntitySpecificPath string
	CreatePR           bool
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
	repository := strings.TrimSpace(in.Repository)
	if repository == "" {
		repository = s.defaultRepository
	}
	if strings.Contains(repository, "/") {
		ownerPart, repoPart, found := strings.Cut(repository, "/")
		if !found || ownerPart == "" || repoPart == "" || strings.Contains(repoPart, "/") {
			return nil, status.Error(codes.InvalidArgument, "repository must be in owner/repo format")
		}
		if owner != "" && ownerPart != owner {
			return nil, status.Error(codes.InvalidArgument, "owner does not match repository")
		}
		if owner == "" {
			owner = ownerPart
		}
		repository = repoPart
	}
	if owner == "" {
		owner = s.defaultOwner
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
		DocumentID:         in.ID,
		FileName:           in.FileName,
		Path:               path,
		PublishPath:        publishPath,
		EntitySpecificPath: strings.TrimSpace(in.EntitySpecificPath),
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

	doc, err := s.repo.GetDocument(ctx, in.DocumentID)
	if err != nil {
		return nil, err
	}
	if doc == nil {
		return nil, errors.New("document not found")
	}
	if doc.OriginalBucket != "" && doc.OriginalBucket != in.Bucket {
		return nil, fmt.Errorf("bucket mismatch: expected %s, got %s", doc.OriginalBucket, in.Bucket)
	}
	if doc.OriginalObjectKey != "" && doc.OriginalObjectKey != in.ObjectKey {
		return nil, fmt.Errorf("object key mismatch: expected %s, got %s", doc.OriginalObjectKey, in.ObjectKey)
	}

	payload := map[string]any{
		"bucket":     in.Bucket,
		"object_key": in.ObjectKey,
	}
	job := &model.ProcessingJob{
		DocumentID: doc.ID,
		Stage:      model.JobStageStoreOriginal,
		Status:     model.JobStatusPending,
		Payload:    payload,
	}
	event := &model.JobEvent{EventType: "scheduled", Message: "confirmed upload"}
	if _, err := s.repo.CreateProcessingJob(ctx, job, event); err != nil {
		reason := fmt.Sprintf("enqueue store_original: %v", err)
		_, _ = s.repo.UpdateStatus(ctx, doc.ID, model.StatusFailed, reason, reason, nil)
		return nil, fmt.Errorf("enqueue processing job: %w", err)
	}

	return doc, nil
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
	updated, err := s.repo.UpdateStatus(ctx, in.DocumentID, in.Status, in.Reason, in.ErrorReason, version)
	if err != nil {
		return nil, err
	}

	s.enqueueNextStage(ctx, updated)
	return updated, nil
}

func (s *Service) enqueueNextStage(ctx context.Context, doc *model.Document) {
	var stage model.JobStage
	switch doc.Status {
	case model.StatusStoredOriginal:
		stage = model.JobStageConvertToMarkdown
	case model.StatusConvertedToMarkdown:
		stage = model.JobStagePublishToGitHub
	default:
		return
	}

	event := &model.JobEvent{EventType: "scheduled", Message: fmt.Sprintf("next stage after %s", doc.Status)}
	_, err := s.repo.CreateProcessingJob(ctx, &model.ProcessingJob{DocumentID: doc.ID, Stage: stage, Status: model.JobStatusPending}, event)
	if err != nil {
		reason := fmt.Sprintf("enqueue %s: %v", stage, err)
		_, _ = s.repo.UpdateStatus(ctx, doc.ID, model.StatusFailed, reason, reason, nil)
	}
}
