package document

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"backend-document-service/internal/domain"
)

// ConfirmUpload проверяет наличие исходного файла и планирует следующую стадию обработки.
type ConfirmUpload struct {
	repo    Repository
	storage Storage
}

func NewConfirmUpload(repo Repository, storage Storage) *ConfirmUpload {
	return &ConfirmUpload{repo: repo, storage: storage}
}

func (uc *ConfirmUpload) Execute(ctx context.Context, in ConfirmUploadInput) (*domain.Document, error) {
	if _, err := uuid.Parse(in.DocumentID); err != nil {
		return nil, errors.New("invalid id")
	}
	if strings.TrimSpace(in.ObjectKey) == "" {
		return nil, errors.New("object_key is required")
	}
	if strings.TrimSpace(in.Bucket) == "" {
		return nil, errors.New("bucket is required")
	}

	doc, err := uc.repo.GetDocument(ctx, in.DocumentID)
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

	if uc.storage == nil {
		return nil, errors.New("storage client is not configured")
	}
	etag, err := uc.storage.ValidateObject(ctx, in.Bucket, in.ObjectKey)
	if err != nil {
		return nil, fmt.Errorf("storage validation failed: %w", err)
	}
	if strings.TrimSpace(etag) == "" {
		return nil, errors.New("etag is required")
	}

	payload := domain.StoreOriginalPayload{Bucket: in.Bucket, ObjectKey: in.ObjectKey, OriginalETag: etag}.ToMap()
	job := &domain.ProcessingJob{
		DocumentID: doc.ID,
		Stage:      domain.JobStageStoreOriginal,
		Status:     domain.JobStatusPending,
		Payload:    payload,
	}
	event := &domain.JobEvent{EventType: "scheduled", Message: "confirmed upload"}
	createdJob, err := uc.repo.CreateProcessingJob(ctx, job, event)
	if err != nil {
		reason := fmt.Sprintf("enqueue store_original: %v", err)
		_, _ = uc.repo.UpdateStatus(ctx, doc.ID, domain.DocumentStatusFailed, reason, reason, nil)
		return nil, fmt.Errorf("enqueue processing job: %w", err)
	}

	if createdJob != nil {
		doc.ActiveJobID = createdJob.TaskID
	}

	return doc, nil
}
