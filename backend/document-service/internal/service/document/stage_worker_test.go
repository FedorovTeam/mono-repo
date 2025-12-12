package document

import (
	"context"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/durationpb"

	"backend-document-service/internal/model"
	githubpublisherv1 "backend-document-service/proto/backend/githubpublisher/v1"
)

type publishRepoStub struct {
	doc            *model.Document
	lastEvent      *model.JobEvent
	lastPayload    map[string]any
	lastStatus     model.JobStatus
	nextRun        *time.Time
	statusUpdates  []model.Status
	lastError      string
	lastUpdateArgs struct {
		reason      string
		errorReason string
	}
}

func (r *publishRepoStub) AcquirePendingJobs(context.Context, model.JobStage, int) ([]model.ProcessingJob, error) {
	return nil, nil
}

func (r *publishRepoStub) UpdateProcessingJob(ctx context.Context, taskID string, status model.JobStatus, nextRunAt *time.Time, incrementAttempt bool, payload map[string]any, event *model.JobEvent) (*model.ProcessingJob, error) {
	r.lastStatus = status
	if payload != nil {
		r.lastPayload = payload
	}
	if nextRunAt != nil {
		r.nextRun = nextRunAt
	}
	r.lastEvent = event
	return &model.ProcessingJob{TaskID: taskID, Status: status, Payload: payload}, nil
}

func (r *publishRepoStub) GetDocument(context.Context, string) (*model.Document, error) {
	return r.doc, nil
}

func (r *publishRepoStub) CreateDocument(context.Context, *model.Document, *model.DocumentVersion, *model.DocumentStatusLog) (*model.Document, error) {
	return nil, nil
}

func (r *publishRepoStub) ListDocuments(context.Context, int, int, *model.Status) ([]model.Document, error) {
	return nil, nil
}

func (r *publishRepoStub) CountStatusLogsWithReasonPrefix(context.Context, string, model.Status, string) (int, error) {
	return 0, nil
}

func (r *publishRepoStub) UpdateStatus(ctx context.Context, id string, to model.Status, reason, errorReason string, version *model.DocumentVersion) (*model.Document, error) {
	if r.doc != nil {
		r.doc.Status = to
		r.doc.ErrorReason = errorReason
		if version != nil && version.MarkdownObjectKey != "" {
			r.doc.MarkdownObjectKey = version.MarkdownObjectKey
		}
	}
	r.statusUpdates = append(r.statusUpdates, to)
	r.lastUpdateArgs.reason = reason
	r.lastUpdateArgs.errorReason = errorReason
	return r.doc, nil
}

func (r *publishRepoStub) LastNonFailedStatus(context.Context, string) (model.Status, error) {
	if r.doc != nil {
		return r.doc.Status, nil
	}
	return model.Status(""), nil
}

func (r *publishRepoStub) ActiveJob(context.Context, string) (*model.ProcessingJob, error) {
	return nil, nil
}

func (r *publishRepoStub) CreateProcessingJob(context.Context, *model.ProcessingJob, *model.JobEvent) (*model.ProcessingJob, error) {
	return nil, nil
}

type staticPublisher struct {
	resp *githubpublisherv1.PublishMarkdownResponse
}

func (s staticPublisher) PublishMarkdown(ctx context.Context, in *githubpublisherv1.PublishMarkdownRequest, opts ...grpc.CallOption) (*githubpublisherv1.PublishMarkdownResponse, error) {
	return s.resp, nil
}

func TestHandlePublishWritesCommitEventPayload(t *testing.T) {
	doc := &model.Document{
		ID:                "doc-commit",
		Name:              "Doc",
		Owner:             "owner",
		Repository:        "repo",
		Branch:            "main",
		BaseBranch:        "main",
		Path:              "docs/doc.md",
		Status:            model.StatusConvertedToMarkdown,
		MarkdownBucket:    "bucket",
		MarkdownObjectKey: "object.md",
		Versions:          []model.DocumentVersion{{Version: 1, MarkdownObjectKey: "object.md"}},
	}

	repo := &publishRepoStub{doc: doc}
	publisher := staticPublisher{resp: &githubpublisherv1.PublishMarkdownResponse{Status: githubpublisherv1.PublishResultStatus_PUBLISH_RESULT_STATUS_SUCCESS, CommitSha: "abc123"}}
	activities := NewActivities(repo, nil, publisher, "owner", "repo", "main", "main", RetryPolicy{InitialBackoff: time.Second, MaxBackoff: time.Minute})
	worker := NewStageWorker(repo, activities, model.JobStagePublishToGitHub, 1, time.Second, &PublishRetryConfig{InitialBackoff: time.Second, MaxBackoff: time.Minute})

	worker.handlePublish(context.Background(), model.ProcessingJob{TaskID: "job-1", DocumentID: doc.ID, Stage: model.JobStagePublishToGitHub})

	if repo.lastEvent == nil {
		t.Fatal("expected job event to be recorded")
	}
	if repo.lastEvent.EventType != "completed" {
		t.Fatalf("unexpected event type: %s", repo.lastEvent.EventType)
	}
	if repo.lastEvent.Payload == nil || repo.lastEvent.Payload["commit_sha"] != "abc123" {
		t.Fatalf("expected commit_sha in event payload, got %#v", repo.lastEvent.Payload)
	}
}

func TestHandlePublishRespectsRetryAfterInFailures(t *testing.T) {
	doc := &model.Document{
		ID:                "doc-retry",
		Name:              "Doc",
		Owner:             "owner",
		Repository:        "repo",
		Branch:            "main",
		BaseBranch:        "main",
		Path:              "docs/doc.md",
		Status:            model.StatusConvertedToMarkdown,
		MarkdownBucket:    "bucket",
		MarkdownObjectKey: "object.md",
		Versions:          []model.DocumentVersion{{Version: 1, MarkdownObjectKey: "object.md"}},
	}

	repo := &publishRepoStub{doc: doc}
	retryAfter := durationpb.New(3 * time.Second)
	publisher := staticPublisher{resp: &githubpublisherv1.PublishMarkdownResponse{
		Status:       githubpublisherv1.PublishResultStatus_PUBLISH_RESULT_STATUS_TEMPORARY_FAILURE,
		ErrorCode:    githubpublisherv1.PublishErrorCode_PUBLISH_ERROR_CODE_TIMEOUT,
		ErrorMessage: "timeout",
		RetryAfter:   retryAfter,
	}}
	activities := NewActivities(repo, nil, publisher, "owner", "repo", "main", "main", RetryPolicy{InitialBackoff: time.Second, MaxBackoff: time.Minute})
	worker := NewStageWorker(repo, activities, model.JobStagePublishToGitHub, 1, time.Second, &PublishRetryConfig{InitialBackoff: time.Second, MaxBackoff: time.Minute})

	start := time.Now()
	worker.handlePublish(context.Background(), model.ProcessingJob{TaskID: "job-2", DocumentID: doc.ID, Stage: model.JobStagePublishToGitHub})

	if repo.lastEvent == nil {
		t.Fatal("expected failed event to be recorded")
	}
	if repo.lastEvent.EventType != "failed" {
		t.Fatalf("unexpected event type: %s", repo.lastEvent.EventType)
	}
	if repo.lastEvent.Payload == nil || repo.lastEvent.Payload["retry_after"] == nil {
		t.Fatalf("expected retry_after hint in payload, got %#v", repo.lastEvent.Payload)
	}
	if repo.nextRun == nil {
		t.Fatal("expected next run to be scheduled")
	}
	delay := repo.nextRun.Sub(start)
	if delay < 2*time.Second || delay > 4*time.Second {
		t.Fatalf("expected retry_after to be respected, got delay=%s", delay)
	}
	if doc.Status != model.StatusFailed {
		t.Fatalf("expected document to be marked failed, got %s", doc.Status)
	}
}
