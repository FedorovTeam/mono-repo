package document

import (
	"context"
	"fmt"
	"log"
	"path/filepath"
	"strings"
	"time"

	"google.golang.org/grpc/metadata"

	"backend-document-service/internal/model"
)

type pendingJobRepo interface {
	AcquirePendingJobs(ctx context.Context, stage model.JobStage, limit int) ([]model.ProcessingJob, error)
	UpdateProcessingJob(ctx context.Context, taskID string, status model.JobStatus, nextRunAt *time.Time, incrementAttempt bool, payload map[string]any, event *model.JobEvent) (*model.ProcessingJob, error)
	GetDocument(ctx context.Context, id string) (*model.Document, error)
}

// StageWorker обрабатывает pending-задачи из processing_jobs.
type StageWorker struct {
	repo         pendingJobRepo
	activities   *Activities
	stage        model.JobStage
	batchSize    int
	retryDelay   time.Duration
	publishRetry *PublishRetryConfig
	metrics      *workerMetrics
	logf         func(format string, args ...interface{})
	waitMarkdown bool
}

type PublishRetryConfig struct {
	InitialBackoff time.Duration
	MaxBackoff     time.Duration
}

func (c PublishRetryConfig) backoff(attempts int) time.Duration {
	if attempts < 1 {
		attempts = 1
	}
	delay := c.InitialBackoff
	for i := 1; i < attempts; i++ {
		delay *= 2
		if delay >= c.MaxBackoff {
			return c.MaxBackoff
		}
	}
	if delay > c.MaxBackoff {
		return c.MaxBackoff
	}
	return delay
}

func NewStageWorker(repo pendingJobRepo, activities *Activities, stage model.JobStage, batchSize int, retryDelay time.Duration, waitMarkdown bool, publishRetry *PublishRetryConfig) *StageWorker {
	if batchSize <= 0 {
		batchSize = 100
	}
	if retryDelay <= 0 {
		retryDelay = time.Minute
	}

	return &StageWorker{
		repo:         repo,
		activities:   activities,
		stage:        stage,
		batchSize:    batchSize,
		retryDelay:   retryDelay,
		publishRetry: publishRetry,
		metrics:      newWorkerMetrics(fmt.Sprintf("stage_%s", stage)),
		logf:         log.Printf,
		waitMarkdown: waitMarkdown,
	}
}

func (w *StageWorker) Run(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = time.Minute
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	w.reconcile(ctx)

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			w.reconcile(ctx)
		}
	}
}

func (w *StageWorker) reconcile(ctx context.Context) {
	for {
		jobs, err := w.repo.AcquirePendingJobs(ctx, w.stage, w.batchSize)
		if err != nil {
			w.logf("stage-worker[%s]: acquire pending jobs: %v", w.stage, err)
			w.metrics.recordError(ctx)
			return
		}
		w.metrics.observeBatch(ctx, len(jobs))
		if len(jobs) == 0 {
			w.metrics.observeBatch(ctx, 0)
			return
		}

		for _, job := range jobs {
			start := time.Now()
			w.handleJob(ctx, job)
			w.metrics.recordDuration(ctx, time.Since(start))
		}

		if len(jobs) < w.batchSize {
			return
		}
	}
}

func (w *StageWorker) handleJob(ctx context.Context, job model.ProcessingJob) {
	switch job.Stage {
	case model.JobStageStoreOriginal:
		w.handleStoreOriginal(ctx, job)
	case model.JobStageConvertToMarkdown:
		w.handleConvert(ctx, job)
	case model.JobStagePublishToGitHub:
		w.handlePublish(ctx, job)
	default:
		w.failJob(ctx, job, fmt.Sprintf("unsupported stage %s", job.Stage))
	}
}

func (w *StageWorker) handleStoreOriginal(ctx context.Context, job model.ProcessingJob) {
	if w.activities == nil {
		w.failJob(ctx, job, "activities not configured")
		return
	}

	if _, err := w.activities.AwaitOriginal(w.jobContext(ctx, job), job.DocumentID); err != nil {
		w.failJob(ctx, job, err.Error())
		return
	}

	if _, err := w.repo.UpdateProcessingJob(ctx, job.TaskID, model.JobStatusCompleted, nil, false, job.Payload, &model.JobEvent{EventType: "completed"}); err != nil {
		w.logf("stage-worker[%s]: update completed job %s: %v", w.stage, job.TaskID, err)
		w.metrics.recordError(ctx)
	}
}

func (w *StageWorker) handleConvert(ctx context.Context, job model.ProcessingJob) {
	if w.activities == nil {
		w.failJob(ctx, job, "activities not configured")
		return
	}

	doc, err := w.repo.GetDocument(ctx, job.DocumentID)
	if err != nil {
		w.failJob(ctx, job, fmt.Sprintf("load document: %v", err))
		return
	}
	if doc == nil {
		w.failJob(ctx, job, fmt.Sprintf("document %s not found", job.DocumentID))
		return
	}

	originalBucket := stringFromPayload(job.Payload, "original_bucket", doc.OriginalBucket)
	originalKey := stringFromPayload(job.Payload, "original_object_key", doc.OriginalObjectKey)
	markdownBucket := stringFromPayload(job.Payload, "markdown_bucket", doc.MarkdownBucket)
	markdownKey := stringFromPayload(job.Payload, "markdown_object_key", resolvedMarkdownKey(doc))

	input := convertInput{
		DocumentID:        job.DocumentID,
		OriginalBucket:    originalBucket,
		MarkdownBucket:    markdownBucket,
		OriginalObjectKey: originalKey,
		MarkdownObjectKey: markdownKey,
		FilenameExtension: strings.TrimPrefix(strings.ToLower(filepath.Ext(originalKey)), "."),
		Version:           latestVersion(doc).Version + 1,
	}

	convCtx := w.jobContext(ctx, job)
	result, err := w.activities.ConvertToMarkdown(convCtx, input)
	if err != nil {
		w.failJob(ctx, job, err.Error())
		return
	}

	job.Payload["markdown_object_key"] = result.MarkdownKey
	if result.QueueID != "" {
		job.Payload["queue_id"] = result.QueueID
	}

	if w.waitMarkdown && !result.Ready {
		awaitInput := awaitMarkdownInput{
			DocumentID:        job.DocumentID,
			MarkdownBucket:    markdownBucket,
			MarkdownObjectKey: result.MarkdownKey,
			CorrelationID:     stringFromPayload(job.Payload, "correlation_id", correlationFromDoc(doc)),
			QueueID:           result.QueueID,
			TransformerStatus: result.Status,
		}
		if err := w.activities.AwaitMarkdown(convCtx, awaitInput); err != nil {
			w.failJob(ctx, job, err.Error())
			return
		}

		result.Ready = true
		if result.Status == "" {
			result.Status = string(model.StatusConvertedToMarkdown)
		}
	}

	if err := w.activities.MarkConverted(convCtx, doc, result.MarkdownKey, result.QueueID, result.Version); err != nil {
		w.failJob(ctx, job, fmt.Sprintf("update status: %v", err))
		return
	}

	if _, err := w.repo.UpdateProcessingJob(ctx, job.TaskID, model.JobStatusCompleted, nil, false, job.Payload, &model.JobEvent{EventType: "completed"}); err != nil {
		w.logf("stage-worker[%s]: complete convert job %s: %v", w.stage, job.TaskID, err)
		w.metrics.recordError(ctx)
	}
}

func (w *StageWorker) handlePublish(ctx context.Context, job model.ProcessingJob) {
	if w.activities == nil {
		w.failJob(ctx, job, "activities not configured")
		return
	}

	doc, err := w.repo.GetDocument(ctx, job.DocumentID)
	if err != nil {
		w.failJob(ctx, job, fmt.Sprintf("load document: %v", err))
		return
	}
	if doc == nil {
		w.failJob(ctx, job, fmt.Sprintf("document %s not found", job.DocumentID))
		return
	}

	repoField := stringFromPayload(job.Payload, "repository", fmt.Sprintf("%s/%s", doc.Owner, doc.Repository))
	owner, repository, _ := strings.Cut(repoField, "/")
	if repository == "" {
		repository = repoField
	}

	markdownKey := stringFromPayload(job.Payload, "markdown_object_key", resolvedMarkdownKey(doc))

	input := publishInput{
		DocumentID:        job.DocumentID,
		DocumentName:      doc.Name,
		Owner:             owner,
		Repository:        repository,
		Branch:            stringFromPayload(job.Payload, "branch", doc.Branch),
		BaseBranch:        stringFromPayload(job.Payload, "base_branch", doc.BaseBranch),
		Path:              stringFromPayload(job.Payload, "github_path", doc.Path),
		CreatePR:          boolFromPayload(job.Payload, "create_pr", doc.CreatePR),
		MarkdownBucket:    stringFromPayload(job.Payload, "markdown_bucket", doc.MarkdownBucket),
		MarkdownObjectKey: markdownKey,
		CorrelationID:     stringFromPayload(job.Payload, "correlation_id", correlationFromDoc(doc)),
		Attempt:           job.Attempts,
	}
	if input.MarkdownObjectKey != "" {
		input.IdempotencyKey = fmt.Sprintf("publish:%s:%s:%s", job.DocumentID, input.Branch, input.MarkdownObjectKey)
	}

	if err := w.activities.PublishMarkdown(w.jobContext(ctx, job), input); err != nil {
		w.failJob(ctx, job, err.Error())
		return
	}

	if _, err := w.repo.UpdateProcessingJob(ctx, job.TaskID, model.JobStatusCompleted, nil, false, job.Payload, &model.JobEvent{EventType: "completed"}); err != nil {
		w.logf("stage-worker[%s]: complete publish job %s: %v", w.stage, job.TaskID, err)
		w.metrics.recordError(ctx)
	}
}

func (w *StageWorker) failJob(ctx context.Context, job model.ProcessingJob, reason string) {
	corr := stringFromPayload(job.Payload, "correlation_id", correlationIDFromContext(ctx))
	nextRun := time.Now().Add(w.retryDelay)
	if w.stage == model.JobStagePublishToGitHub && w.publishRetry != nil {
		if backoff := w.publishRetry.backoff(job.Attempts); backoff > 0 {
			nextRun = time.Now().Add(backoff)
		}
	}
	if job.Payload == nil {
		job.Payload = map[string]any{}
	}
	job.Payload["last_error"] = reason
	if _, err := w.repo.UpdateProcessingJob(ctx, job.TaskID, model.JobStatusFailed, &nextRun, false, job.Payload, &model.JobEvent{EventType: "failed", Message: reason}); err != nil {
		w.logf("stage-worker[%s]: fail job %s correlation_id=%s: %v (original reason: %s)", w.stage, job.TaskID, corr, err, reason)
		w.metrics.recordError(ctx)
	}
}

func (w *StageWorker) jobContext(ctx context.Context, job model.ProcessingJob) context.Context {
	corr := stringFromPayload(job.Payload, "correlation_id", correlationIDFromContext(ctx))
	if corr == "" {
		return ctx
	}
	return metadataAppend(ctx, corr)
}

func metadataAppend(ctx context.Context, correlationID string) context.Context {
	return metadata.AppendToOutgoingContext(ctx, correlationIDHeader, correlationID)
}

func stringFromPayload(payload map[string]any, key, fallback string) string {
	if payload == nil {
		return fallback
	}
	if val, ok := payload[key]; ok {
		if s, ok := val.(string); ok && strings.TrimSpace(s) != "" {
			return s
		}
	}
	return fallback
}

func boolFromPayload(payload map[string]any, key string, fallback bool) bool {
	if payload == nil {
		return fallback
	}
	if val, ok := payload[key]; ok {
		if b, ok := val.(bool); ok {
			return b
		}
	}
	return fallback
}
