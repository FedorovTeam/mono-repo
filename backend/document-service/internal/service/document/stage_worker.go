package document

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/durationpb"

	"backend-document-service/internal/domain"
	workersvc "backend-document-service/internal/usecase"
	githubpublisherv1 "backend-document-service/proto/backend/githubpublisher/v1"
)

// StageWorker обрабатывает pending-задачи из processing_jobs.
type StageWorker struct {
	repo       Repository
	activities *Activities
	stage      domain.JobStage
	batchSize  int
	metrics    *workerMetrics
	service    *workersvc.WorkerService
	retryDelay time.Duration
}

func NewStageWorker(repo Repository, activities *Activities, stage domain.JobStage, batchSize int, retryDelay time.Duration) *StageWorker {
	if batchSize <= 0 {
		batchSize = 100
	}
	if retryDelay <= 0 {
		retryDelay = time.Minute
	}

	service := workersvc.NewWorkerService(repo, repo, retryDelay)
	service.SetLogger(log.Printf)

	return &StageWorker{
		repo:       repo,
		activities: activities,
		stage:      stage,
		batchSize:  batchSize,
		metrics:    newWorkerMetrics(fmt.Sprintf("stage_%s", stage)),
		service:    service,
		retryDelay: retryDelay,
	}
}

func (w *StageWorker) Run(ctx context.Context, interval time.Duration) {
	handler := func(ctx context.Context, job domain.ProcessingJob) (workersvc.JobResult, error) {
		start := time.Now()
		result, err := w.handleJob(ctx, job)
		w.metrics.recordDuration(ctx, time.Since(start))
		if err != nil {
			w.metrics.recordError(ctx)
		}
		return result, err
	}

	w.service.Run(ctx, w.stage, interval, w.batchSize, handler, w.metrics.observeBatch)
}

func (w *StageWorker) handleJob(ctx context.Context, job domain.ProcessingJob) (workersvc.JobResult, error) {
	switch job.Stage {
	case domain.JobStageStoreOriginal:
		return w.handleStoreOriginal(ctx, job)
	case domain.JobStageConvertToMarkdown:
		return w.handleConvert(ctx, job)
	case domain.JobStagePublishToGitHub:
		return w.handlePublish(ctx, job)
	default:
		return workersvc.JobResult{Payload: job.Payload}, fmt.Errorf("unsupported stage %s", job.Stage)
	}
	return workersvc.JobResult{Payload: job.Payload}, nil
}

func (w *StageWorker) handleStoreOriginal(ctx context.Context, job domain.ProcessingJob) (workersvc.JobResult, error) {
	payload := clonePayload(job.Payload)
	if w.activities == nil {
		return workersvc.JobResult{Payload: payload}, errors.New("activities not configured")
	}

	if err := w.activities.MarkStoredOriginal(w.jobContext(ctx, job), job.DocumentID); err != nil {
		return workersvc.JobResult{Payload: payload}, err
	}

	return workersvc.JobResult{Payload: payload, Event: &domain.JobEvent{EventType: "completed"}}, nil
}

func (w *StageWorker) handleConvert(ctx context.Context, job domain.ProcessingJob) (workersvc.JobResult, error) {
	payload := clonePayload(job.Payload)
	if w.activities == nil {
		return workersvc.JobResult{Payload: payload}, errors.New("activities not configured")
	}

	doc, err := w.repo.GetDocument(ctx, job.DocumentID)
	if err != nil {
		return workersvc.JobResult{Payload: payload}, fmt.Errorf("load document: %w", err)
	}
	if doc == nil {
		return workersvc.JobResult{Payload: payload}, fmt.Errorf("document %s not found", job.DocumentID)
	}

	originalBucket := stringFromPayload(payload, "original_bucket", doc.OriginalBucket)
	originalKey := stringFromPayload(payload, "original_object_key", doc.OriginalObjectKey)
	markdownBucket := stringFromPayload(payload, "markdown_bucket", doc.MarkdownBucket)
	markdownKey := stringFromPayload(payload, "markdown_object_key", resolvedMarkdownKey(doc))

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
		if status.Code(err) == codes.ResourceExhausted {
			next := time.Now().Add(w.retryDelay)
			payload["last_error"] = err.Error()
			return workersvc.JobResult{
				Payload:          payload,
				Status:           domain.JobStatusPending,
				NextRun:          &next,
				IncrementAttempt: true,
				Event:            &domain.JobEvent{EventType: "postponed", Message: err.Error()},
			}, nil
		}
		return workersvc.JobResult{Payload: payload}, err
	}

	payload["markdown_object_key"] = result.MarkdownKey
	if queueID := strings.TrimSpace(result.QueueID); queueID != "" {
		payload["queue_id"] = queueID
	}
	if status := strings.TrimSpace(result.Status); status != "" {
		payload["status"] = status
	}

	if !result.Ready {
		next := time.Now().Add(w.retryDelay)
		return workersvc.JobResult{
			Payload: payload,
			NextRun: &next,
			Status:  domain.JobStatusPending,
			Event:   &domain.JobEvent{EventType: "postponed", Message: fmt.Sprintf("status=%s", result.Status)},
		}, nil
	}

	if err := w.activities.MarkConverted(convCtx, doc, result.MarkdownKey, result.QueueID, result.Version); err != nil {
		return workersvc.JobResult{Payload: payload}, fmt.Errorf("update status: %w", err)
	}

	return workersvc.JobResult{Payload: payload, Event: &domain.JobEvent{EventType: "completed"}}, nil
}

func (w *StageWorker) handlePublish(ctx context.Context, job domain.ProcessingJob) (workersvc.JobResult, error) {
	payload := clonePayload(job.Payload)
	if w.activities == nil {
		return workersvc.JobResult{Payload: payload}, errors.New("activities not configured")
	}

	doc, err := w.repo.GetDocument(ctx, job.DocumentID)
	if err != nil {
		return workersvc.JobResult{Payload: payload}, fmt.Errorf("load document: %w", err)
	}
	if doc == nil {
		return workersvc.JobResult{Payload: payload}, fmt.Errorf("document %s not found", job.DocumentID)
	}

	repoField := stringFromPayload(payload, "repository", fmt.Sprintf("%s/%s", doc.Owner, doc.Repository))
	owner, repository, _ := strings.Cut(repoField, "/")
	if repository == "" {
		repository = repoField
	}

	markdownKey := stringFromPayload(payload, "markdown_object_key", resolvedMarkdownKey(doc))
	commitMessage := stringFromPayload(payload, "commit_message", pickCommitMessage(doc, PublishDefaults{}))
	authorName := stringFromPayload(payload, "author_name", "")
	authorEmail := stringFromPayload(payload, "author_email", "")
	prTitle := stringFromPayload(payload, "pr_title", pickPRTitle(doc, PublishDefaults{}))
	prBody := stringFromPayload(payload, "pr_body", "")

	input := publishInput{
		DocumentID:        job.DocumentID,
		DocumentName:      doc.Name,
		Owner:             owner,
		Repository:        repository,
		Branch:            stringFromPayload(payload, "branch", doc.Branch),
		BaseBranch:        stringFromPayload(payload, "base_branch", doc.BaseBranch),
		Path:              stringFromPayload(payload, "github_path", doc.Path),
		CreatePR:          boolFromPayload(payload, "create_pr", doc.CreatePR),
		MarkdownBucket:    stringFromPayload(payload, "markdown_bucket", doc.MarkdownBucket),
		MarkdownObjectKey: markdownKey,
		CorrelationID:     stringFromPayload(payload, "correlation_id", correlationFromDoc(doc)),
		CommitMessage:     commitMessage,
		AuthorName:        authorName,
		AuthorEmail:       authorEmail,
		PRTitle:           prTitle,
		PRBody:            prBody,
	}

	if strings.TrimSpace(input.CommitMessage) == "" {
		return workersvc.JobResult{Payload: payload}, errors.New("commit_message is required for publish")
	}
	if strings.TrimSpace(input.AuthorName) == "" || strings.TrimSpace(input.AuthorEmail) == "" {
		return workersvc.JobResult{Payload: payload}, errors.New("author_name and author_email are required for publish")
	}
	if input.MarkdownObjectKey != "" {
		input.IdempotencyKey = fmt.Sprintf("publish:%s:%s:%s", job.DocumentID, input.Branch, input.MarkdownObjectKey)
	}

	payload["markdown_bucket"] = input.MarkdownBucket
	payload["markdown_object_key"] = input.MarkdownObjectKey
	payload["repository"] = fmt.Sprintf("%s/%s", owner, repository)
	payload["branch"] = input.Branch
	payload["base_branch"] = input.BaseBranch
	payload["github_path"] = input.Path
	payload["create_pr"] = input.CreatePR
	payload["commit_message"] = input.CommitMessage
	payload["pr_title"] = input.PRTitle
	payload["pr_body"] = input.PRBody
	payload["author_name"] = input.AuthorName
	payload["author_email"] = input.AuthorEmail
	payload["correlation_id"] = input.CorrelationID

	resp, err := w.activities.PublishMarkdown(w.jobContext(ctx, job), input)
	eventPayload := buildPublishEventPayload(resp)
	result := workersvc.JobResult{Payload: payload, Event: &domain.JobEvent{Payload: eventPayload, EventType: "completed"}}
	if err != nil {
		result.Event.EventType = "failed"
		result.Event.Message = err.Error()
		if retryAfter := pickRetryAfter(resp); retryAfter != nil && retryAfter.AsDuration() > 0 {
			next := time.Now().Add(retryAfter.AsDuration())
			result.NextRun = &next
			if result.Event.Payload == nil {
				result.Event.Payload = map[string]any{}
			}
			result.Event.Payload["retry_after"] = retryAfter.AsDuration().String()
			result.Payload["retry_after"] = retryAfter.AsDuration().String()
		}
		return result, err
	}

	return result, nil
}

func clonePayload(src map[string]any) map[string]any {
	if src == nil {
		return map[string]any{}
	}
	dst := make(map[string]any, len(src))
	for k, v := range src {
		dst[k] = v
	}
	return dst
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
