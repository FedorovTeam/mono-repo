package document

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"backend-document-service/internal/domain"
	storagerepo "backend-document-service/internal/infrastructure/storage"
	documentusecase "backend-document-service/internal/usecase/document"
	storagev1 "backend-document-service/proto/backend/storage/v1"
)

type Repository interface {
	CreateDocument(ctx context.Context, doc *domain.Document, version *domain.DocumentVersion, log *domain.DocumentStatusLog) (*domain.Document, error)
	GetDocument(ctx context.Context, id string) (*domain.Document, error)
	ListDocuments(ctx context.Context, limit, offset int, status *domain.Status) ([]domain.Document, error)
	CountStatusLogsWithReasonPrefix(ctx context.Context, id string, status domain.Status, reasonPrefix string) (int, error)
	UpdateStatus(ctx context.Context, id string, to domain.Status, reason, errorReason string, version *domain.DocumentVersion) (*domain.Document, error)
	LastNonFailedStatus(ctx context.Context, id string) (domain.Status, error)
	ActiveJob(ctx context.Context, documentID string) (*domain.ProcessingJob, error)
	CreateProcessingJob(ctx context.Context, job *domain.ProcessingJob, event *domain.JobEvent) (*domain.ProcessingJob, error)
	UpdateProcessingJob(ctx context.Context, taskID string, status domain.JobStatus, nextRunAt *time.Time, incrementAttempt bool, payload map[string]any, event *domain.JobEvent) (*domain.ProcessingJob, error)
	RequeueStaleProcessingJobs(ctx context.Context, staleBefore time.Time, limit int, nextRunAt time.Time, reason string) ([]domain.ProcessingJob, error)
}

type Service struct {
	repo                  Repository
	storage               domain.Storage
	defaultOriginalBucket string
	defaultMarkdownBucket string
	defaultOwner          string
	defaultRepository     string
	defaultBranch         string
	defaultBaseBranch     string
	createDocumentUC      *documentusecase.CreateDocument
	confirmUploadUC       *documentusecase.ConfirmUpload
	updateStatusUC        *documentusecase.UpdateStatus
}

func New(repo Repository, _ *Activities, storage storagev1.StorageServiceClient, defaultOriginalBucket, defaultMarkdownBucket, defaultOwner, defaultRepository, defaultBranch, defaultBaseBranch string) *Service {
	storageAdapter := storagerepo.New(storage)
	defaults := documentusecase.Defaults{
		OriginalBucket: defaultOriginalBucket,
		MarkdownBucket: defaultMarkdownBucket,
		Owner:          defaultOwner,
		Repository:     defaultRepository,
		Branch:         defaultBranch,
		BaseBranch:     defaultBaseBranch,
	}
	return &Service{
		repo:                  repo,
		storage:               storageAdapter,
		defaultOriginalBucket: defaultOriginalBucket,
		defaultMarkdownBucket: defaultMarkdownBucket,
		defaultOwner:          defaultOwner,
		defaultRepository:     defaultRepository,
		defaultBranch:         defaultBranch,
		defaultBaseBranch:     defaultBaseBranch,
		createDocumentUC: &documentusecase.CreateDocument{
			Repo:     repo,
			Storage:  storageAdapter,
			Defaults: defaults,
		},
		confirmUploadUC: &documentusecase.ConfirmUpload{
			Repo:    repo,
			Storage: storageAdapter,
		},
		updateStatusUC: &documentusecase.UpdateStatus{
			Repo:    repo,
			Storage: storageAdapter,
		},
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
	CreatePR          bool
}

type ConfirmUploadInput struct {
	DocumentID string
	Bucket     string
	ObjectKey  string
}

type UpdateStatusInput struct {
	DocumentID        string
	Status            domain.Status
	Reason            string
	ErrorReason       string
	MarkdownObjectKey string
	OriginalObjectKey string
	Version           int32
}

func (s *Service) CreateDocument(ctx context.Context, in CreateInput) (*domain.Document, error) {
	doc, err := s.createDocumentUC.Execute(ctx, documentusecase.CreateInput(in))
	if err != nil {
		return nil, err
	}
	return doc, nil
}

func (s *Service) ConfirmUpload(ctx context.Context, in ConfirmUploadInput) (*domain.Document, error) {
	if s.storage == nil {
		return nil, errors.New("storage client is not configured")
	}

	doc, err := s.repo.GetDocument(ctx, in.DocumentID)
	if err != nil {
		return nil, err
	}
	if doc == nil {
		return nil, errors.New("document not found")
	}
	if doc.Status != domain.StatusUploaded {
		return doc, nil
	}

	if err := s.storage.ValidateObject(ctx, in.Bucket, in.ObjectKey); err != nil {
		return nil, fmt.Errorf("storage validation failed: %w", err)
	}

	doc, err = s.confirmUploadUC.Execute(ctx, documentusecase.ConfirmUploadInput(in))
	if err != nil {
		return nil, err
	}

	payload := map[string]any{
		"bucket":     in.Bucket,
		"object_key": in.ObjectKey,
	}
	job := &domain.ProcessingJob{
		DocumentID: doc.ID,
		Stage:      domain.JobStageStoreOriginal,
		Status:     domain.JobStatusPending,
		Payload:    payload,
	}
	event := &domain.JobEvent{EventType: "scheduled", Message: "confirmed upload"}
	createdJob, err := s.repo.CreateProcessingJob(ctx, job, event)
	if err != nil {
		reason := fmt.Sprintf("enqueue store_original: %v", err)
		_, _ = s.repo.UpdateStatus(ctx, doc.ID, domain.StatusFailed, reason, reason, nil)
		return nil, fmt.Errorf("enqueue processing job: %w", err)
	}

	if createdJob != nil {
		doc.ActiveJobID = createdJob.TaskID
	}

	return doc, nil
}

func (s *Service) GetDocument(ctx context.Context, id string) (*domain.Document, error) {
	if _, err := uuid.Parse(id); err != nil {
		return nil, errors.New("invalid id")
	}
	return s.repo.GetDocument(ctx, id)
}

func (s *Service) ListDocuments(ctx context.Context, limit, offset int, status *domain.Status) ([]domain.Document, error) {
	if status != nil {
		if err := domain.ValidateStatus(*status); err != nil {
			return nil, err
		}
	}
	return s.repo.ListDocuments(ctx, limit, offset, status)
}

func (s *Service) UpdateStatus(ctx context.Context, in UpdateStatusInput) (*domain.Document, error) {
	if in.ErrorReason == "" && in.Status == domain.StatusFailed {
		in.ErrorReason = in.Reason
	}

	updated, err := s.updateStatusUC.Execute(ctx, documentusecase.UpdateStatusInput(in))
	if err != nil {
		return nil, err
	}

	if job := s.enqueueNextStage(ctx, updated); job != nil {
		updated.ActiveJobID = job.TaskID
	} else if activeJob, err := s.repo.ActiveJob(ctx, updated.ID); err == nil && activeJob != nil {
		updated.ActiveJobID = activeJob.TaskID
	}
	return updated, nil
}

func (s *Service) enqueueNextStage(ctx context.Context, doc *domain.Document) *domain.ProcessingJob {
	var stage domain.JobStage
	switch doc.Status {
	case domain.StatusStoredOriginal:
		stage = domain.JobStageConvertToMarkdown
	case domain.StatusConvertedToMarkdown:
		stage = domain.JobStagePublishToGitHub
	default:
		return nil
	}

	event := &domain.JobEvent{EventType: "scheduled", Message: fmt.Sprintf("next stage after %s", doc.Status)}
	job, err := s.repo.CreateProcessingJob(ctx, &domain.ProcessingJob{DocumentID: doc.ID, Stage: stage, Status: domain.JobStatusPending}, event)
	if err != nil {
		reason := fmt.Sprintf("enqueue %s: %v", stage, err)
		_, _ = s.repo.UpdateStatus(ctx, doc.ID, domain.StatusFailed, reason, reason, nil)
		return nil
	}

	return job
}
