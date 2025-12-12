package document

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/types/known/durationpb"

	"backend-document-service/internal/domain"
	githubpublisherv1 "backend-document-service/proto/backend/githubpublisher/v1"
)

type pendingJobRepo interface {
	AcquirePendingJobs(ctx context.Context, stage domain.JobStage, limit int) ([]domain.ProcessingJob, error)
	UpdateProcessingJob(ctx context.Context, taskID string, status domain.JobStatus, nextRunAt *time.Time, incrementAttempt bool, payload map[string]any, event *domain.JobEvent) (*domain.ProcessingJob, error)
	GetDocument(ctx context.Context, id string) (*domain.Document, error)
}

// StageWorker обрабатывает pending-задачи из processing_jobs.
type StageWorker struct {
	repo       pendingJobRepo
	activities *Activities
	stage      domain.JobStage
	batchSize  int
	retryDelay time.Duration
	metrics    *workerMetrics
	logf       func(format string, args ...interface{})
}

func NewStageWorker(repo pendingJobRepo, activities *Activities, stage domain.JobStage, batchSize int, retryDelay time.Duration) *StageWorker {
	if batchSize <= 0 {
		batchSize = 100
	}
	if retryDelay <= 0 {
		retryDelay = time.Minute
	}

	return &StageWorker{
		repo:       repo,
		activities: activities,
		stage:      stage,
		batchSize:  batchSize,
		retryDelay: retryDelay,
		metrics:    newWorkerMetrics(fmt.Sprintf("stage_%s", stage)),
		logf:       log.Printf,
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

func (w *StageWorker) handleJob(ctx context.Context, job domain.ProcessingJob) {
	switch job.Stage {
	case domain.JobStageStoreOriginal:
		w.handleStoreOriginal(ctx, job)
	case domain.JobStageConvertToMarkdown:
		w.handleConvert(ctx, job)
	case domain.JobStagePublishToGitHub:
		w.handlePublish(ctx, job)
	default:
		w.failJob(ctx, job, fmt.Sprintf("unsupported stage %s", job.Stage), nil, nil)
	}
}

func (w *StageWorker) handleStoreOriginal(ctx context.Context, job domain.ProcessingJob) {
	if w.activities == nil {
		w.failJob(ctx, job, "activities not configured", nil, nil)
		return
	}

	if err := w.activities.MarkStoredOriginal(w.jobContext(ctx, job), job.DocumentID); err != nil {
		w.failJob(ctx, job, err.Error(), nil, nil)
		return
	}

	if _, err := w.repo.UpdateProcessingJob(ctx, job.TaskID, domain.JobStatusCompleted, nil, false, job.Payload, &domain.JobEvent{EventType: "completed"}); err != nil {
		w.logf("stage-worker[%s]: update completed job %s: %v", w.stage, job.TaskID, err)
		w.metrics.recordError(ctx)
	}
}

func (w *StageWorker) handleConvert(ctx context.Context, job domain.ProcessingJob) {
	if w.activities == nil {
		w.failJob(ctx, job, "activities not configured", nil, nil)
		return
	}

	doc, err := w.repo.GetDocument(ctx, job.DocumentID)
	if err != nil {
		w.failJob(ctx, job, fmt.Sprintf("load document: %v", err), nil, nil)
		return
	}
	if doc == nil {
		w.failJob(ctx, job, fmt.Sprintf("document %s not found", job.DocumentID), nil, nil)
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
		Version:           latestVersion(doc).Version + 1,
	}

	convCtx := w.jobContext(ctx, job)
	result, err := w.activities.ConvertToMarkdown(convCtx, input)
	if err != nil {
		w.failJob(ctx, job, err.Error(), nil, nil)
		return
	}

	job.Payload["markdown_object_key"] = result.MarkdownKey

	if !result.Ready {
		w.failJob(ctx, job, "conversion did not finish", nil, nil)
		return
	}

	if err := w.activities.MarkConverted(convCtx, doc, result.MarkdownKey, result.QueueID, result.Version); err != nil {
		w.failJob(ctx, job, fmt.Sprintf("update status: %v", err), nil, nil)
		return
	}

	if _, err := w.repo.UpdateProcessingJob(ctx, job.TaskID, domain.JobStatusCompleted, nil, false, job.Payload, &domain.JobEvent{EventType: "completed"}); err != nil {
		w.logf("stage-worker[%s]: complete convert job %s: %v", w.stage, job.TaskID, err)
		w.metrics.recordError(ctx)
	}
}

func (w *StageWorker) handlePublish(ctx context.Context, job domain.ProcessingJob) {
	if w.activities == nil {
		w.failJob(ctx, job, "activities not configured", nil, nil)
		return
	}

	doc, err := w.repo.GetDocument(ctx, job.DocumentID)
	if err != nil {
		w.failJob(ctx, job, fmt.Sprintf("load document: %v", err), nil, nil)
		return
	}
	if doc == nil {
		w.failJob(ctx, job, fmt.Sprintf("document %s not found", job.DocumentID), nil, nil)
		return
	}

	repoField := stringFromPayload(job.Payload, "repository", fmt.Sprintf("%s/%s", doc.Owner, doc.Repository))
	owner, repository, _ := strings.Cut(repoField, "/")
	if repository == "" {
		repository = repoField
	}

	markdownKey := stringFromPayload(job.Payload, "markdown_object_key", resolvedMarkdownKey(doc))
	commitMessage := stringFromPayload(job.Payload, "commit_message", pickCommitMessage(doc, PublishDefaults{}))
	authorName := stringFromPayload(job.Payload, "author_name", "")
	authorEmail := stringFromPayload(job.Payload, "author_email", "")
	prTitle := stringFromPayload(job.Payload, "pr_title", pickPRTitle(doc, PublishDefaults{}))
	prBody := stringFromPayload(job.Payload, "pr_body", "")

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
		CommitMessage:     commitMessage,
		AuthorName:        authorName,
		AuthorEmail:       authorEmail,
		PRTitle:           prTitle,
		PRBody:            prBody,
	}

	if strings.TrimSpace(input.CommitMessage) == "" {
		w.failJob(ctx, job, "commit_message is required for publish", nil, nil)
		return
	}
	if strings.TrimSpace(input.AuthorName) == "" || strings.TrimSpace(input.AuthorEmail) == "" {
		w.failJob(ctx, job, "author_name and author_email are required for publish", nil, nil)
		return
	}
	if input.MarkdownObjectKey != "" {
		input.IdempotencyKey = fmt.Sprintf("publish:%s:%s:%s", job.DocumentID, input.Branch, input.MarkdownObjectKey)
	}

	if job.Payload == nil {
		job.Payload = map[string]any{}
	}
	job.Payload["markdown_bucket"] = input.MarkdownBucket
	job.Payload["markdown_object_key"] = input.MarkdownObjectKey
	job.Payload["repository"] = fmt.Sprintf("%s/%s", owner, repository)
	job.Payload["branch"] = input.Branch
	job.Payload["base_branch"] = input.BaseBranch
	job.Payload["github_path"] = input.Path
	job.Payload["create_pr"] = input.CreatePR
	job.Payload["commit_message"] = input.CommitMessage
	job.Payload["pr_title"] = input.PRTitle
	job.Payload["pr_body"] = input.PRBody
	job.Payload["author_name"] = input.AuthorName
	job.Payload["author_email"] = input.AuthorEmail
	job.Payload["correlation_id"] = input.CorrelationID

	resp, err := w.activities.PublishMarkdown(w.jobContext(ctx, job), input)
	eventPayload := buildPublishEventPayload(resp)
	if err != nil {
		w.failJob(ctx, job, err.Error(), pickRetryAfter(resp), eventPayload)
		return
	}

	if _, err := w.repo.UpdateProcessingJob(ctx, job.TaskID, domain.JobStatusCompleted, nil, false, job.Payload, &domain.JobEvent{EventType: "completed", Payload: eventPayload}); err != nil {
		w.logf("stage-worker[%s]: complete publish job %s: %v", w.stage, job.TaskID, err)
		w.metrics.recordError(ctx)
	}
}

func (w *StageWorker) failJob(ctx context.Context, job domain.ProcessingJob, reason string, retryAfter *durationpb.Duration, eventPayload map[string]any) {
	corr := stringFromPayload(job.Payload, "correlation_id", correlationIDFromContext(ctx))
	nextRun := time.Now().Add(w.retryDelay)
	if retryAfter != nil && retryAfter.AsDuration() > 0 {
		nextRun = time.Now().Add(retryAfter.AsDuration())
	}
	if job.Payload == nil {
		job.Payload = map[string]any{}
	}
	job.Payload["last_error"] = reason
	if retryAfter != nil {
		job.Payload["retry_after"] = retryAfter.AsDuration().String()
	}
	if eventPayload == nil {
		eventPayload = map[string]any{}
	}
	if retryAfter != nil && retryAfter.AsDuration() > 0 {
		eventPayload["retry_after"] = retryAfter.AsDuration().String()
	}
	if _, err := w.repo.UpdateProcessingJob(ctx, job.TaskID, domain.JobStatusFailed, &nextRun, false, job.Payload, &domain.JobEvent{EventType: "failed", Message: reason, Payload: eventPayload}); err != nil {
		w.logf("stage-worker[%s]: fail job %s correlation_id=%s: %v (original reason: %s)", w.stage, job.TaskID, corr, err, reason)
		w.metrics.recordError(ctx)
	}
}

func buildPublishEventPayload(resp *githubpublisherv1.PublishMarkdownResponse) map[string]any {
	if resp == nil {
		return nil
	}
	payload := map[string]any{}
	if sha := strings.TrimSpace(resp.GetCommitSha()); sha != "" {
		payload["commit_sha"] = sha
	}
	if resp.GetPullRequestNumber() > 0 {
		payload["pull_request_number"] = resp.GetPullRequestNumber()
	}
	if url := strings.TrimSpace(resp.GetPullRequestUrl()); url != "" {
		payload["pull_request_url"] = url
	}
	if resp.GetIdempotencyHit() {
		payload["idempotency_hit"] = true
	}
	if retry := resp.GetRetryAfter(); retry != nil && retry.AsDuration() > 0 {
		payload["retry_after"] = retry.AsDuration().String()
	}
	if len(payload) == 0 {
		return nil
	}
	return payload
}

func pickRetryAfter(resp *githubpublisherv1.PublishMarkdownResponse) *durationpb.Duration {
	if resp == nil {
		return nil
	}
	return resp.GetRetryAfter()
}

func (w *StageWorker) jobContext(ctx context.Context, job domain.ProcessingJob) context.Context {
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
