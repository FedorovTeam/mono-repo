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
	"backend-document-service/internal/model"
	"backend-document-service/internal/usecase"
	githubpublisherv1 "backend-document-service/proto/backend/githubpublisher/v1"
)

type stageWorkerRepo interface {
	domain.JobRepository
	domain.DocumentRepository
}

// StageWorker обрабатывает pending-задачи из processing_jobs.
type StageWorker struct {
	repo       stageWorkerRepo
	activities *Activities
	worker     *usecase.WorkerService
	stage      model.JobStage
	batchSize  int
	retryDelay time.Duration
	metrics    *workerMetrics
	logf       func(format string, args ...interface{})
}

func NewStageWorker(repo stageWorkerRepo, activities *Activities, stage model.JobStage, batchSize int, retryDelay time.Duration) *StageWorker {
	if batchSize <= 0 {
		batchSize = 100
	}
	if retryDelay <= 0 {
		retryDelay = time.Minute
	}

	metrics := newWorkerMetrics(fmt.Sprintf("stage_%s", stage))
	hooks := usecase.WorkerHooks{
		Batch: func(ctx context.Context, size int) { metrics.observeBatch(ctx, size) },
		Error: func(ctx context.Context) { metrics.recordError(ctx) },
	}

	return &StageWorker{
		repo:       repo,
		activities: activities,
		worker:     usecase.NewWorkerService(repo, batchSize, hooks),
		stage:      stage,
		batchSize:  batchSize,
		retryDelay: retryDelay,
		metrics:    metrics,
		logf:       log.Printf,
	}
}

func (w *StageWorker) Run(ctx context.Context, interval time.Duration) {
	w.worker.Run(ctx, w.stage, interval, w.handleJobWithMetrics)
}

func (w *StageWorker) reconcile(ctx context.Context) {
	w.worker.Process(ctx, w.stage, w.handleJobWithMetrics)
}

func (w *StageWorker) handleJobWithMetrics(ctx context.Context, job model.ProcessingJob) usecase.JobResult {
	start := time.Now()
	result := w.handleJob(ctx, job)
	w.metrics.recordDuration(ctx, time.Since(start))
	return result
}

func (w *StageWorker) handleJob(ctx context.Context, job model.ProcessingJob) usecase.JobResult {
	switch job.Stage {
	case model.JobStageStoreOriginal:
		return w.handleStoreOriginal(ctx, job)
	case model.JobStageConvertToMarkdown:
		return w.handleConvert(ctx, job)
	case model.JobStagePublishToGitHub:
		return w.handlePublish(ctx, job)
	default:
		return w.failJob(ctx, job, fmt.Sprintf("unsupported stage %s", job.Stage), nil, nil)
	}
}

func (w *StageWorker) handleStoreOriginal(ctx context.Context, job model.ProcessingJob) usecase.JobResult {
	if w.activities == nil {
		return w.failJob(ctx, job, "activities not configured", nil, nil)
	}

	if err := w.activities.MarkStoredOriginal(w.jobContext(ctx, job), job.DocumentID); err != nil {
		return w.failJob(ctx, job, err.Error(), nil, nil)
	}

	return usecase.JobResult{Status: model.JobStatusCompleted, Payload: job.Payload, Event: &model.JobEvent{EventType: "completed"}}
}

func (w *StageWorker) handleConvert(ctx context.Context, job model.ProcessingJob) usecase.JobResult {
	if w.activities == nil {
		return w.failJob(ctx, job, "activities not configured", nil, nil)
	}

	doc, err := w.repo.GetDocument(ctx, job.DocumentID)
	if err != nil {
		return w.failJob(ctx, job, fmt.Sprintf("load document: %v", err), nil, nil)
	}
	if doc == nil {
		return w.failJob(ctx, job, fmt.Sprintf("document %s not found", job.DocumentID), nil, nil)
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
		return w.failJob(ctx, job, err.Error(), nil, nil)
	}

	if job.Payload == nil {
		job.Payload = map[string]any{}
	}
	job.Payload["markdown_object_key"] = result.MarkdownKey

	if !result.Ready {
		return w.failJob(ctx, job, "conversion did not finish", nil, nil)
	}

	if err := w.activities.MarkConverted(convCtx, doc, result.MarkdownKey, result.QueueID, result.Version); err != nil {
		return w.failJob(ctx, job, fmt.Sprintf("update status: %v", err), nil, nil)
	}

	return usecase.JobResult{Status: model.JobStatusCompleted, Payload: job.Payload, Event: &model.JobEvent{EventType: "completed"}}
}

func (w *StageWorker) handlePublish(ctx context.Context, job model.ProcessingJob) usecase.JobResult {
	if w.activities == nil {
		return w.failJob(ctx, job, "activities not configured", nil, nil)
	}

	doc, err := w.repo.GetDocument(ctx, job.DocumentID)
	if err != nil {
		return w.failJob(ctx, job, fmt.Sprintf("load document: %v", err), nil, nil)
	}
	if doc == nil {
		return w.failJob(ctx, job, fmt.Sprintf("document %s not found", job.DocumentID), nil, nil)
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
		return w.failJob(ctx, job, "commit_message is required for publish", nil, nil)
	}
	if strings.TrimSpace(input.AuthorName) == "" || strings.TrimSpace(input.AuthorEmail) == "" {
		return w.failJob(ctx, job, "author_name and author_email are required for publish", nil, nil)
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
		return w.failJob(ctx, job, err.Error(), pickRetryAfter(resp), eventPayload)
	}

	return usecase.JobResult{Status: model.JobStatusCompleted, Payload: job.Payload, Event: &model.JobEvent{EventType: "completed", Payload: eventPayload}}
}

func (w *StageWorker) failJob(ctx context.Context, job model.ProcessingJob, reason string, retryAfter *durationpb.Duration, eventPayload map[string]any) usecase.JobResult {
	if job.Payload == nil {
		job.Payload = map[string]any{}
	}

	nextRun := w.retryDelay
	if retryAfter != nil && retryAfter.AsDuration() > 0 {
		nextRun = retryAfter.AsDuration()
		job.Payload["retry_after"] = retryAfter.AsDuration().String()
	}
	job.Payload["last_error"] = reason

	if eventPayload == nil {
		eventPayload = map[string]any{}
	}
	if retryAfter != nil && retryAfter.AsDuration() > 0 {
		eventPayload["retry_after"] = retryAfter.AsDuration().String()
	}

	return usecase.JobResult{
		Status:     model.JobStatusFailed,
		Payload:    job.Payload,
		RetryAfter: nextRun,
		Event:      &model.JobEvent{EventType: "failed", Message: reason, Payload: eventPayload},
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
