package document

import (
	"context"
	"fmt"
	"strings"
	"time"

	"backend-document-service/internal/domain"
)

type jobFollowerRepo interface {
	ListFinishedJobs(ctx context.Context, limit int, postpone time.Duration) ([]domain.ProcessingJob, error)
	GetDocument(ctx context.Context, id string) (*domain.Document, error)
	CreateProcessingJob(ctx context.Context, job *domain.ProcessingJob, event *domain.JobEvent) (*domain.ProcessingJob, error)
	UpdateStatus(ctx context.Context, id string, to domain.DocumentStatus, reason, errorReason string, version *domain.DocumentVersion) (*domain.Document, error)
	UpdateProcessingJob(ctx context.Context, taskID string, status domain.JobStatus, nextRunAt *time.Time, incrementAttempt bool, payload map[string]any, event *domain.JobEvent) (*domain.ProcessingJob, error)
}

type PublishDefaults struct {
	CommitMessage string
	PRTitle       string
	PRBody        string
	AuthorName    string
	AuthorEmail   string
}

// JobFollower читает завершённые задачи из processing_jobs и
// запускает следующую стадию либо помечает документ как Failed.
type JobFollower struct {
	repo      jobFollowerRepo
	batchSize int
	postpone  time.Duration
	metrics   *workerMetrics
	defaults  PublishDefaults
}

func NewJobFollower(repo jobFollowerRepo, batchSize int, postpone time.Duration, defaults PublishDefaults) *JobFollower {
	if batchSize <= 0 {
		batchSize = 100
	}
	if postpone <= 0 {
		postpone = 5 * time.Minute
	}

	return &JobFollower{
		repo:      repo,
		batchSize: batchSize,
		postpone:  postpone,
		metrics:   newWorkerMetrics("job_follower"),
		defaults:  defaults,
	}
}

func (f *JobFollower) Run(ctx context.Context, interval time.Duration) error {
	if interval <= 0 {
		interval = time.Minute
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	if err := f.reconcile(ctx); err != nil {
		return err
	}

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if err := f.reconcile(ctx); err != nil {
				return err
			}
		}
	}
}

func (f *JobFollower) reconcile(ctx context.Context) error {
	for {
		jobs, err := f.repo.ListFinishedJobs(ctx, f.batchSize, f.postpone)
		if err != nil {
			f.metrics.recordError(ctx)
			return err
		}
		f.metrics.observeBatch(ctx, len(jobs))
		if len(jobs) == 0 {
			f.metrics.observeBatch(ctx, 0)
			return nil
		}

		for _, job := range jobs {
			if err := f.handleJob(ctx, job); err != nil {
				return err
			}
		}

		if len(jobs) < f.batchSize {
			return nil
		}
	}
}

func (f *JobFollower) handleJob(ctx context.Context, job domain.ProcessingJob) error {
	start := time.Now()
	defer f.metrics.recordDuration(ctx, time.Since(start))

	doc, err := f.repo.GetDocument(ctx, job.DocumentID)
	if err != nil {
		f.metrics.recordError(ctx)
		return err
	}
	if doc == nil {
		f.metrics.recordError(ctx)
		return fmt.Errorf("document %s not found for job %s", job.DocumentID, job.TaskID)
	}

	switch job.Status {
	case domain.JobStatusCompleted:
		return f.scheduleNext(ctx, job, doc)
	case domain.JobStatusFailed:
		return f.markFailed(ctx, job, doc)
	}

	return nil
}

func (f *JobFollower) scheduleNext(ctx context.Context, job domain.ProcessingJob, doc *domain.Document) error {
	if doc.Status == domain.DocumentStatusFailed {
		return nil
	}

	switch job.Stage {
	case domain.JobStageStoreOriginal:
		payload := domain.ConvertPayload{
			OriginalBucket:     doc.OriginalBucket,
			OriginalObjectKey:  doc.OriginalObjectKey,
			MarkdownBucket:     doc.MarkdownBucket,
			MarkdownObjectKey:  doc.MarkdownObjectKey,
			ForceOCR:           boolPtr(doc.ForceOCR),
			JobPayloadMetadata: domain.JobPayloadMetadata{CorrelationID: correlationFromDoc(doc)},
		}.ToMap()
		event := &domain.JobEvent{EventType: "scheduled", Message: "next stage: convert", Payload: map[string]any{"force_ocr": doc.ForceOCR}}
		if _, err := f.repo.CreateProcessingJob(ctx, &domain.ProcessingJob{DocumentID: doc.ID, Stage: domain.JobStageConvertToMarkdown, Status: domain.JobStatusPending, Payload: payload}, event); err != nil {
			return fmt.Errorf("enqueue convert for %s: %w", doc.ID, err)
		}
	case domain.JobStageConvertToMarkdown:
		payload := domain.PublishPayload{
			MarkdownBucket:     doc.MarkdownBucket,
			MarkdownObjectKey:  doc.MarkdownObjectKey,
			Repository:         fmt.Sprintf("%s/%s", doc.Owner, doc.Repository),
			Branch:             doc.Branch,
			BaseBranch:         doc.BaseBranch,
			GitHubPath:         doc.Path,
			CreatePR:           boolPtr(doc.CreatePR),
			CommitMessage:      pickCommitMessage(doc, f.defaults),
			PRTitle:            pickPRTitle(doc, f.defaults),
			PRBody:             f.defaults.PRBody,
			AuthorName:         f.defaults.AuthorName,
			AuthorEmail:        f.defaults.AuthorEmail,
			JobPayloadMetadata: domain.JobPayloadMetadata{CorrelationID: correlationFromDoc(doc)},
		}.ToMap()
		event := &domain.JobEvent{EventType: "scheduled", Message: "next stage: publish"}
		if _, err := f.repo.CreateProcessingJob(ctx, &domain.ProcessingJob{DocumentID: doc.ID, Stage: domain.JobStagePublishToGitHub, Status: domain.JobStatusPending, Payload: payload}, event); err != nil {
			return fmt.Errorf("enqueue publish for %s: %w", doc.ID, err)
		}
	}

	return nil
}

func (f *JobFollower) markFailed(ctx context.Context, job domain.ProcessingJob, doc *domain.Document) error {
	reason := fmt.Sprintf("stage %s failed", job.Stage)
	if lastError, ok := job.Payload["last_error"].(string); ok && lastError != "" {
		reason = fmt.Sprintf("%s: %s", reason, lastError)
	}

	if _, err := f.repo.UpdateStatus(ctx, doc.ID, domain.DocumentStatusFailed, reason, reason, nil); err != nil {
		return fmt.Errorf("mark failed document %s: %w", doc.ID, err)
	}

	if _, err := f.repo.UpdateProcessingJob(ctx, job.TaskID, job.Status, nil, false, job.Payload, &domain.JobEvent{EventType: "handled", Message: "marked document failed"}); err != nil {
		return fmt.Errorf("update failed job %s: %w", job.TaskID, err)
	}

	return nil
}

func pickCommitMessage(doc *domain.Document, defaults PublishDefaults) string {
	if defaults.CommitMessage != "" {
		return defaults.CommitMessage
	}
	if doc != nil && strings.TrimSpace(doc.Name) != "" {
		return fmt.Sprintf("Update %s", doc.Name)
	}
	return "Update document"
}

func pickPRTitle(doc *domain.Document, defaults PublishDefaults) string {
	if defaults.PRTitle != "" {
		return defaults.PRTitle
	}
	if doc != nil {
		return doc.Name
	}
	return ""
}

func correlationFromDoc(doc *domain.Document) string {
	if doc.WorkflowID != "" {
		return doc.WorkflowID
	}
	return doc.ID
}
