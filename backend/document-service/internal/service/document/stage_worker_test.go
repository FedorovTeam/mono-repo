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
	jobs           map[string]model.ProcessingJob
	now            time.Time
	recoveryEvents []model.JobEvent
}

func (r *publishRepoStub) AcquirePendingJobs(ctx context.Context, _ model.JobStage, limit int) ([]model.ProcessingJob, error) {
	if len(r.jobs) == 0 {
		return nil, nil
	}

	_ = ctx
	now := r.now
	if now.IsZero() {
		now = time.Now()
	}

	var result []model.ProcessingJob
	for _, job := range r.jobs {
		if job.Status != model.JobStatusPending {
			continue
		}
		if !job.NextRunAt.IsZero() && job.NextRunAt.After(now) {
			continue
		}
		result = append(result, job)
		if limit > 0 && len(result) >= limit {
			break
		}
	}
	return result, nil
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
	if r.jobs != nil {
		job := r.jobs[taskID]
		if incrementAttempt {
			job.Attempts++
		}
		job.Status = status
		if nextRunAt != nil {
			job.NextRunAt = *nextRunAt
		}
		job.Payload = payload
		r.jobs[taskID] = job
	}
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

func (r *publishRepoStub) RequeueStaleProcessingJobs(ctx context.Context, staleBefore time.Time, limit int, nextRunAt time.Time, reason string) ([]model.ProcessingJob, error) {
	if len(r.jobs) == 0 {
		return nil, nil
	}
	now := r.now
	if now.IsZero() {
		now = time.Now()
	}
	var restored []model.ProcessingJob
	for id, job := range r.jobs {
		if job.Status != model.JobStatusInProgress || job.UpdatedAt.After(staleBefore) || len(restored) >= limit {
			continue
		}
		job.Status = model.JobStatusPending
		job.Attempts++
		job.NextRunAt = nextRunAt
		job.UpdatedAt = now
		r.jobs[id] = job
		restored = append(restored, job)
		r.recoveryEvents = append(r.recoveryEvents, model.JobEvent{EventType: "recovered", Message: reason})
	}
	return restored, nil
}

type staticPublisher struct {
	resp        *githubpublisherv1.PublishMarkdownResponse
	lastRequest *githubpublisherv1.PublishMarkdownRequest
}

func (s *staticPublisher) PublishMarkdown(ctx context.Context, in *githubpublisherv1.PublishMarkdownRequest, opts ...grpc.CallOption) (*githubpublisherv1.PublishMarkdownResponse, error) {
	s.lastRequest = in
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
	publisher := &staticPublisher{resp: &githubpublisherv1.PublishMarkdownResponse{Status: githubpublisherv1.PublishResultStatus_PUBLISH_RESULT_STATUS_SUCCESS, CommitSha: "abc123"}}
	activities := NewActivities(repo, nil, publisher)
	worker := NewStageWorker(repo, activities, model.JobStagePublishToGitHub, 1, time.Second)

	worker.handlePublish(context.Background(), model.ProcessingJob{TaskID: "job-1", DocumentID: doc.ID, Stage: model.JobStagePublishToGitHub, Payload: map[string]any{
		"commit_message": "commit",
		"author_name":    "Author",
		"author_email":   "author@example.com",
	}})

	if repo.lastEvent == nil {
		t.Fatal("expected job event to be recorded")
	}
	if repo.lastEvent.EventType != "completed" {
		t.Fatalf("unexpected event type: %s", repo.lastEvent.EventType)
	}
	if repo.lastEvent.Payload == nil || repo.lastEvent.Payload["commit_sha"] != "abc123" {
		t.Fatalf("expected commit_sha in event payload, got %#v", repo.lastEvent.Payload)
	}
	if publisher.lastRequest == nil || publisher.lastRequest.GetCommitMessage() != "commit" || publisher.lastRequest.GetAuthorName() != "Author" {
		t.Fatalf("expected commit metadata to be propagated, got %#v", publisher.lastRequest)
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
	publisher := &staticPublisher{resp: &githubpublisherv1.PublishMarkdownResponse{
		Status:       githubpublisherv1.PublishResultStatus_PUBLISH_RESULT_STATUS_TEMPORARY_FAILURE,
		ErrorCode:    githubpublisherv1.PublishErrorCode_PUBLISH_ERROR_CODE_TIMEOUT,
		ErrorMessage: "timeout",
		RetryAfter:   retryAfter,
	}}
	activities := NewActivities(repo, nil, publisher)
	worker := NewStageWorker(repo, activities, model.JobStagePublishToGitHub, 1, time.Second)

	start := time.Now()
	worker.handlePublish(context.Background(), model.ProcessingJob{TaskID: "job-2", DocumentID: doc.ID, Stage: model.JobStagePublishToGitHub, Payload: map[string]any{
		"commit_message": "commit",
		"author_name":    "Author",
		"author_email":   "author@example.com",
	}})

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

func TestRecoveredJobPickedByStageWorker(t *testing.T) {
	fixed := time.Date(2024, 12, 1, 12, 0, 0, 0, time.UTC)
	doc := &model.Document{
		ID:                "doc-recover",
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

	repo := &publishRepoStub{
		doc: doc,
		now: fixed,
		jobs: map[string]model.ProcessingJob{
			"job-stuck": {
				TaskID:     "job-stuck",
				DocumentID: doc.ID,
				Stage:      model.JobStagePublishToGitHub,
				Status:     model.JobStatusInProgress,
				Attempts:   1,
				Payload: map[string]any{
					"commit_message": "commit",
					"author_name":    "Author",
					"author_email":   "author@example.com",
				},
				UpdatedAt: fixed.Add(-time.Minute),
			},
		},
	}

	reconciler := NewProcessingReconciler(repo, 2, 30*time.Second, 15*time.Second)
	reconciler.now = func() time.Time { return repo.now }
	reconciler.reconcile(context.Background())

	recovered := repo.jobs["job-stuck"]
	if recovered.Status != model.JobStatusPending {
		t.Fatalf("expected job to be re-queued, got %s", recovered.Status)
	}
	if len(repo.recoveryEvents) == 0 {
		t.Fatalf("expected recovery event to be recorded")
	}

	repo.now = fixed.Add(time.Minute)
	publisher := &staticPublisher{resp: &githubpublisherv1.PublishMarkdownResponse{Status: githubpublisherv1.PublishResultStatus_PUBLISH_RESULT_STATUS_SUCCESS, CommitSha: "commit-sha"}}
	activities := NewActivities(repo, nil, publisher)
	worker := NewStageWorker(repo, activities, model.JobStagePublishToGitHub, 2, time.Second)

	worker.reconcile(context.Background())

	completed := repo.jobs["job-stuck"]
	if completed.Status != model.JobStatusCompleted {
		t.Fatalf("expected job to be completed after recovery, got %s", completed.Status)
	}
	if doc.Status != model.StatusPublishedToGitHub {
		t.Fatalf("expected document to be published, got %s", doc.Status)
	}
}
