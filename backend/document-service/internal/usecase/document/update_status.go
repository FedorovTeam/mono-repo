package document

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"

	"backend-document-service/internal/domain"
)

// UpdateStatus обновляет статус документа и планирует следующий этап пайплайна.
type UpdateStatus struct {
	repo       Repository
	scheduler  JobScheduler
	statusRepo StatusUpdater
}

func NewUpdateStatus(repo Repository) *UpdateStatus {
	return &UpdateStatus{repo: repo, scheduler: repo, statusRepo: repo}
}

func (uc *UpdateStatus) Execute(ctx context.Context, in UpdateStatusInput) (*domain.Document, error) {
	if _, err := uuid.Parse(in.DocumentID); err != nil {
		return nil, errors.New("invalid id")
	}

	status := domain.DocumentStatus(in.Status)
	if err := domain.ValidateDocumentStatus(status); err != nil {
		return nil, err
	}
	if in.ErrorReason == "" && status == domain.DocumentStatusFailed {
		in.ErrorReason = in.Reason
	}

	var version *domain.DocumentVersion
	if in.MarkdownObjectKey != "" || in.OriginalObjectKey != "" || in.Version > 0 {
		version = &domain.DocumentVersion{
			Version:           in.Version,
			OriginalObjectKey: in.OriginalObjectKey,
			MarkdownObjectKey: in.MarkdownObjectKey,
		}
		if version.Version == 0 {
			version.Version = 1
		}
	}

	updated, err := uc.statusRepo.UpdateStatus(ctx, in.DocumentID, status, in.Reason, in.ErrorReason, version)
	if err != nil {
		return nil, err
	}

	if job := uc.enqueueNextStage(ctx, updated); job != nil {
		updated.ActiveJobID = job.TaskID
	} else if activeJob, err := uc.scheduler.ActiveJob(ctx, updated.ID); err == nil && activeJob != nil {
		updated.ActiveJobID = activeJob.TaskID
	}
	return updated, nil
}

func (uc *UpdateStatus) enqueueNextStage(ctx context.Context, doc *domain.Document) *domain.ProcessingJob {
	var stage domain.JobStage
	switch doc.Status {
	case domain.DocumentStatusStoredOriginal:
		stage = domain.JobStageConvertToMarkdown
	case domain.DocumentStatusConvertedToMarkdown:
		stage = domain.JobStagePublishToGitHub
	default:
		return nil
	}

	event := &domain.JobEvent{EventType: "scheduled", Message: fmt.Sprintf("next stage after %s", doc.Status)}
	job, err := uc.scheduler.CreateProcessingJob(ctx, &domain.ProcessingJob{DocumentID: doc.ID, Stage: stage, Status: domain.JobStatusPending}, event)
	if err != nil {
		reason := fmt.Sprintf("enqueue %s: %v", stage, err)
		_, _ = uc.statusRepo.UpdateStatus(ctx, doc.ID, domain.DocumentStatusFailed, reason, reason, nil)
		return nil
	}

	return job
}
