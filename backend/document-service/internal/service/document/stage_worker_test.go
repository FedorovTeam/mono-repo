package document

import (
	"context"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/durationpb"

	"backend-document-service/internal/domain"
	githubpublisherv1 "backend-document-service/proto/backend/githubpublisher/v1"
	transformerv1 "backend-document-service/proto/backend/transformer/v1"
)

type publishRepoStub struct {
	doc            *domain.Document
	lastEvent      *domain.JobEvent
	lastPayload    map[string]any
	lastStatus     domain.JobStatus
	nextRun        *time.Time
	statusUpdates  []domain.DocumentStatus
	lastError      string
	lastUpdateArgs struct {
		reason      string
		errorReason string
	}
	jobs           map[string]domain.ProcessingJob
	now            time.Time
	recoveryEvents []domain.JobEvent
}

func (r *publishRepoStub) AcquirePendingJobs(ctx context.Context, _ domain.JobStage, limit int) ([]domain.ProcessingJob, error) {
	if len(r.jobs) == 0 {
		return nil, nil
	}

	_ = ctx
	now := r.now
	if now.IsZero() {
		now = time.Now()
	}

	var result []domain.ProcessingJob
	for _, job := range r.jobs {
		if job.Status != domain.JobStatusPending {
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

func (r *publishRepoStub) UpdateProcessingJob(ctx context.Context, taskID string, status domain.JobStatus, nextRunAt *time.Time, incrementAttempt bool, payload map[string]any, event *domain.JobEvent) (*domain.ProcessingJob, error) {
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
	return &domain.ProcessingJob{TaskID: taskID, Status: status, Payload: payload}, nil
}

func (r *publishRepoStub) GetDocument(context.Context, string) (*domain.Document, error) {
	return r.doc, nil
}

func (r *publishRepoStub) CreateDocument(context.Context, *domain.Document, *domain.DocumentVersion, *domain.DocumentStatusLog) (*domain.Document, error) {
	return nil, nil
}

func (r *publishRepoStub) ListDocuments(context.Context, int, int, *domain.DocumentStatus) ([]domain.Document, error) {
	return nil, nil
}

func (r *publishRepoStub) CountStatusLogsWithReasonPrefix(context.Context, string, domain.DocumentStatus, string) (int, error) {
	return 0, nil
}

func (r *publishRepoStub) UpdateStatus(ctx context.Context, id string, to domain.DocumentStatus, reason, errorReason string, version *domain.DocumentVersion) (*domain.Document, error) {
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

func (r *publishRepoStub) LastNonFailedStatus(context.Context, string) (domain.DocumentStatus, error) {
	if r.doc != nil {
		return r.doc.Status, nil
	}
	return domain.DocumentStatus(""), nil
}

func (r *publishRepoStub) ActiveJob(context.Context, string) (*domain.ProcessingJob, error) {
	return nil, nil
}

func (r *publishRepoStub) AddJobEvent(context.Context, *domain.JobEvent) error { return nil }

func (r *publishRepoStub) CreateProcessingJob(context.Context, *domain.ProcessingJob, *domain.JobEvent) (*domain.ProcessingJob, error) {
	return nil, nil
}

func (r *publishRepoStub) RequeueStaleProcessingJobs(ctx context.Context, staleBefore time.Time, limit int, nextRunAt time.Time, reason string) ([]domain.ProcessingJob, error) {
	if len(r.jobs) == 0 {
		return nil, nil
	}
	now := r.now
	if now.IsZero() {
		now = time.Now()
	}
	var restored []domain.ProcessingJob
	for id, job := range r.jobs {
		if job.Status != domain.JobStatusInProgress || job.UpdatedAt.After(staleBefore) || len(restored) >= limit {
			continue
		}
		job.Status = domain.JobStatusPending
		job.Attempts++
		job.NextRunAt = nextRunAt
		job.UpdatedAt = now
		r.jobs[id] = job
		restored = append(restored, job)
		r.recoveryEvents = append(r.recoveryEvents, domain.JobEvent{EventType: "recovered", Message: reason})
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

type staticTransformer struct {
	resp *transformerv1.ConvertDocumentToMarkdownResponse
}

func (s *staticTransformer) ConvertDocumentToMarkdown(ctx context.Context, in *transformerv1.ConvertDocumentToMarkdownRequest, opts ...grpc.CallOption) (*transformerv1.ConvertDocumentToMarkdownResponse, error) {
	_ = in
	return s.resp, nil
}

func (*staticTransformer) ConvertDocxToMarkdown(context.Context, *transformerv1.ConvertDocxToMarkdownRequest, ...grpc.CallOption) (*transformerv1.ConvertDocxToMarkdownResponse, error) {
	return nil, status.Error(codes.Unimplemented, "not implemented")
}

type errorTransformer struct {
	err error
}

func (s *errorTransformer) ConvertDocumentToMarkdown(context.Context, *transformerv1.ConvertDocumentToMarkdownRequest, ...grpc.CallOption) (*transformerv1.ConvertDocumentToMarkdownResponse, error) {
	return nil, s.err
}

func (*errorTransformer) ConvertDocxToMarkdown(context.Context, *transformerv1.ConvertDocxToMarkdownRequest, ...grpc.CallOption) (*transformerv1.ConvertDocxToMarkdownResponse, error) {
	return nil, status.Error(codes.Unimplemented, "not implemented")
}

func TestHandlePublishWritesCommitEventPayload(t *testing.T) {
	doc := &domain.Document{
		ID:                "doc-commit",
		Name:              "Doc",
		Owner:             "owner",
		Repository:        "repo",
		Branch:            "main",
		BaseBranch:        "main",
		Path:              "docs/doc.md",
		Status:            domain.DocumentStatusConvertedToMarkdown,
		MarkdownBucket:    "bucket",
		MarkdownObjectKey: "object.md",
		Versions:          []domain.DocumentVersion{{Version: 1, MarkdownObjectKey: "object.md"}},
	}

	repo := &publishRepoStub{doc: doc}
	publisher := &staticPublisher{resp: &githubpublisherv1.PublishMarkdownResponse{Status: githubpublisherv1.PublishResultStatus_PUBLISH_RESULT_STATUS_SUCCESS, CommitSha: "abc123"}}
	activities := NewActivities(repo, nil, publisher)
	worker := NewStageWorker(repo, activities, domain.JobStagePublishToGitHub, 1, time.Second)

	result, err := worker.handlePublish(context.Background(), domain.ProcessingJob{TaskID: "job-1", DocumentID: doc.ID, Stage: domain.JobStagePublishToGitHub, Payload: map[string]any{
		"commit_message": "commit",
		"author_name":    "Author",
		"author_email":   "author@example.com",
	}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Event == nil || result.Event.EventType != "completed" {
		t.Fatalf("expected completed event, got %#v", result.Event)
	}
	if result.Event.Payload == nil || result.Event.Payload["commit_sha"] != "abc123" {
		t.Fatalf("expected commit_sha in event payload, got %#v", result.Event.Payload)
	}
	if publisher.lastRequest == nil || publisher.lastRequest.GetCommitMessage() != "commit" || publisher.lastRequest.GetAuthorName() != "Author" {
		t.Fatalf("expected commit metadata to be propagated, got %#v", publisher.lastRequest)
	}
}

func TestHandlePublishRespectsRetryAfterInFailures(t *testing.T) {
	doc := &domain.Document{
		ID:                "doc-retry",
		Name:              "Doc",
		Owner:             "owner",
		Repository:        "repo",
		Branch:            "main",
		BaseBranch:        "main",
		Path:              "docs/doc.md",
		Status:            domain.DocumentStatusConvertedToMarkdown,
		MarkdownBucket:    "bucket",
		MarkdownObjectKey: "object.md",
		Versions:          []domain.DocumentVersion{{Version: 1, MarkdownObjectKey: "object.md"}},
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
	worker := NewStageWorker(repo, activities, domain.JobStagePublishToGitHub, 1, time.Second)

	start := time.Now()
	result, err := worker.handlePublish(context.Background(), domain.ProcessingJob{TaskID: "job-2", DocumentID: doc.ID, Stage: domain.JobStagePublishToGitHub, Payload: map[string]any{
		"commit_message": "commit",
		"author_name":    "Author",
		"author_email":   "author@example.com",
	}})
	if err == nil {
		t.Fatal("expected error from publisher")
	}

	if result.Event == nil || result.Event.EventType != "failed" {
		t.Fatalf("unexpected event: %#v", result.Event)
	}
	if result.Event.Payload == nil || result.Event.Payload["retry_after"] == nil {
		t.Fatalf("expected retry_after hint in payload, got %#v", result.Event.Payload)
	}
	if result.NextRun == nil {
		t.Fatal("expected next run to be scheduled")
	}
	delay := result.NextRun.Sub(start)
	if delay < 2*time.Second || delay > 4*time.Second {
		t.Fatalf("expected retry_after to be respected, got delay=%s", delay)
	}
}

func TestRecoveredJobPickedByStageWorker(t *testing.T) {
	fixed := time.Date(2024, 12, 1, 12, 0, 0, 0, time.UTC)
	doc := &domain.Document{
		ID:                "doc-recover",
		Name:              "Doc",
		Owner:             "owner",
		Repository:        "repo",
		Branch:            "main",
		BaseBranch:        "main",
		Path:              "docs/doc.md",
		Status:            domain.DocumentStatusConvertedToMarkdown,
		MarkdownBucket:    "bucket",
		MarkdownObjectKey: "object.md",
		Versions:          []domain.DocumentVersion{{Version: 1, MarkdownObjectKey: "object.md"}},
	}

	repo := &publishRepoStub{
		doc: doc,
		now: fixed,
		jobs: map[string]domain.ProcessingJob{
			"job-stuck": {
				TaskID:     "job-stuck",
				DocumentID: doc.ID,
				Stage:      domain.JobStagePublishToGitHub,
				Status:     domain.JobStatusInProgress,
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
	reconciler.reconcile(context.Background(), time.Second)

	recovered := repo.jobs["job-stuck"]
	if recovered.Status != domain.JobStatusPending {
		t.Fatalf("expected job to be re-queued, got %s", recovered.Status)
	}
	if len(repo.recoveryEvents) == 0 {
		t.Fatalf("expected recovery event to be recorded")
	}

	repo.now = fixed.Add(time.Minute)
	publisher := &staticPublisher{resp: &githubpublisherv1.PublishMarkdownResponse{Status: githubpublisherv1.PublishResultStatus_PUBLISH_RESULT_STATUS_SUCCESS, CommitSha: "commit-sha"}}
	activities := NewActivities(repo, nil, publisher)
	worker := NewStageWorker(repo, activities, domain.JobStagePublishToGitHub, 2, time.Second)

	ctx, cancel := context.WithCancel(context.Background())
	go worker.Run(ctx, time.Second)

	waitUntil(t, func() bool {
		completed := repo.jobs["job-stuck"]
		return completed.Status == domain.JobStatusCompleted && doc.Status == domain.DocumentStatusPublishedToGitHub
	})
	cancel()
}

func TestHandleConvertPostponesPendingStatuses(t *testing.T) {
	now := time.Now()
	doc := &domain.Document{
		ID:                "doc-pending",
		Name:              "Doc",
		Owner:             "owner",
		Repository:        "repo",
		Branch:            "main",
		BaseBranch:        "main",
		Path:              "docs/doc.md",
		Status:            domain.DocumentStatusStoredOriginal,
		OriginalBucket:    "orig-bucket",
		OriginalObjectKey: "path/doc.docx",
		MarkdownBucket:    "md-bucket",
		MarkdownObjectKey: "path/doc.md",
		Versions:          []domain.DocumentVersion{{Version: 1, OriginalObjectKey: "path/doc.docx", MarkdownObjectKey: "path/doc.md"}},
	}

	repo := &publishRepoStub{doc: doc, now: now}
	transformer := &staticTransformer{resp: &transformerv1.ConvertDocumentToMarkdownResponse{QueueId: "queue-1", Status: "pending"}}
	activities := NewActivities(repo, transformer, nil)
	worker := NewStageWorker(repo, activities, domain.JobStageConvertToMarkdown, 1, time.Second)

	result, err := worker.handleConvert(context.Background(), domain.ProcessingJob{TaskID: "job-1", DocumentID: doc.ID, Stage: domain.JobStageConvertToMarkdown})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Status != domain.JobStatusPending {
		t.Fatalf("expected pending status, got %s", result.Status)
	}
	if result.NextRun == nil || result.NextRun.Before(now.Add(900*time.Millisecond)) {
		t.Fatalf("expected postponed next run, got %v", result.NextRun)
	}
	if result.Payload["queue_id"] != "queue-1" {
		t.Fatalf("expected queue_id in payload, got %v", result.Payload)
	}
	if result.Payload["status"] != "pending" {
		t.Fatalf("expected transformer status in payload, got %v", result.Payload)
	}
}

func TestHandleConvertMarksFailedStatus(t *testing.T) {
	doc := &domain.Document{
		ID:                "doc-failed-status",
		Name:              "Doc",
		Owner:             "owner",
		Repository:        "repo",
		Branch:            "main",
		BaseBranch:        "main",
		Path:              "docs/doc.md",
		Status:            domain.DocumentStatusStoredOriginal,
		OriginalBucket:    "orig-bucket",
		OriginalObjectKey: "path/doc.docx",
		MarkdownBucket:    "md-bucket",
		MarkdownObjectKey: "path/doc.md",
		Versions:          []domain.DocumentVersion{{Version: 1, OriginalObjectKey: "path/doc.docx", MarkdownObjectKey: "path/doc.md"}},
	}

	repo := &publishRepoStub{doc: doc, jobs: map[string]domain.ProcessingJob{
		"job-failed": {
			TaskID:     "job-failed",
			DocumentID: doc.ID,
			Stage:      domain.JobStageConvertToMarkdown,
			Status:     domain.JobStatusPending,
			Payload: map[string]any{
				"original_bucket":     doc.OriginalBucket,
				"original_object_key": doc.OriginalObjectKey,
				"markdown_bucket":     doc.MarkdownBucket,
				"markdown_object_key": doc.MarkdownObjectKey,
			},
		},
	}}
	transformer := &staticTransformer{resp: &transformerv1.ConvertDocumentToMarkdownResponse{QueueId: "queue-2", Status: "failed"}}
	activities := NewActivities(repo, transformer, nil)
	worker := NewStageWorker(repo, activities, domain.JobStageConvertToMarkdown, 1, time.Second)

	ctx, cancel := context.WithCancel(context.Background())
	go worker.Run(ctx, 10*time.Millisecond)
	waitUntil(t, func() bool { return repo.lastStatus == domain.JobStatusFailed })
	cancel()

	if repo.lastPayload == nil || repo.lastPayload["last_error"] == nil {
		t.Fatalf("expected last_error to be stored in payload, got %v", repo.lastPayload)
	}
	if doc.Status != domain.DocumentStatusFailed {
		t.Fatalf("expected document to be marked failed, got %s", doc.Status)
	}
	if len(repo.statusUpdates) == 0 || repo.statusUpdates[len(repo.statusUpdates)-1] != domain.DocumentStatusFailed {
		t.Fatalf("expected failed status update to be recorded, got %v", repo.statusUpdates)
	}
}

func TestHandleConvertReschedulesOnResourceExhausted(t *testing.T) {
	now := time.Now()
	doc := &domain.Document{
		ID:                "doc-exhausted",
		Name:              "Doc",
		Owner:             "owner",
		Repository:        "repo",
		Branch:            "main",
		BaseBranch:        "main",
		Path:              "docs/doc.md",
		Status:            domain.DocumentStatusStoredOriginal,
		OriginalBucket:    "orig-bucket",
		OriginalObjectKey: "path/doc.docx",
		MarkdownBucket:    "md-bucket",
		MarkdownObjectKey: "path/doc.md",
		Versions:          []domain.DocumentVersion{{Version: 1, OriginalObjectKey: "path/doc.docx", MarkdownObjectKey: "path/doc.md"}},
	}

	repo := &publishRepoStub{doc: doc, now: now}
	transformer := &errorTransformer{err: status.Error(codes.ResourceExhausted, "queue limit reached")}
	activities := NewActivities(repo, transformer, nil)
	worker := NewStageWorker(repo, activities, domain.JobStageConvertToMarkdown, 1, time.Second)

	result, err := worker.handleConvert(context.Background(), domain.ProcessingJob{TaskID: "job-exhausted", DocumentID: doc.ID, Stage: domain.JobStageConvertToMarkdown, Payload: map[string]any{}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Status != domain.JobStatusPending {
		t.Fatalf("expected pending status, got %s", result.Status)
	}
	if result.NextRun == nil || !result.NextRun.After(time.Now().Add(-time.Millisecond)) {
		t.Fatalf("expected postponed next run, got %v", result.NextRun)
	}
	if !result.IncrementAttempt {
		t.Fatalf("expected attempts increment on postpone")
	}
	if result.Event == nil || result.Event.EventType != "postponed" {
		t.Fatalf("expected postponed event, got %#v", result.Event)
	}
	if result.Payload == nil || result.Payload["last_error"] == nil {
		t.Fatalf("expected last_error in payload, got %v", result.Payload)
	}
	if doc.Status != domain.DocumentStatusStoredOriginal {
		t.Fatalf("document status should remain unchanged, got %s", doc.Status)
	}
}

func TestHandleConvertRetriesOnTemporaryGRPCErrors(t *testing.T) {
	now := time.Now()
	doc := &domain.Document{
		ID:                "doc-temporary",
		Name:              "Doc",
		Owner:             "owner",
		Repository:        "repo",
		Branch:            "main",
		BaseBranch:        "main",
		Path:              "docs/doc.md",
		Status:            domain.DocumentStatusStoredOriginal,
		OriginalBucket:    "orig-bucket",
		OriginalObjectKey: "path/doc.docx",
		MarkdownBucket:    "md-bucket",
		MarkdownObjectKey: "path/doc.md",
		Versions:          []domain.DocumentVersion{{Version: 1, OriginalObjectKey: "path/doc.docx", MarkdownObjectKey: "path/doc.md"}},
	}

	repo := &publishRepoStub{doc: doc, now: now}
	transformer := &errorTransformer{err: status.Error(codes.Unavailable, "transformer unavailable")}
	activities := NewActivities(repo, transformer, nil)
	worker := NewStageWorker(repo, activities, domain.JobStageConvertToMarkdown, 1, 200*time.Millisecond)

	start := time.Now()
	result, err := worker.handleConvert(context.Background(), domain.ProcessingJob{TaskID: "job-temp", DocumentID: doc.ID, Stage: domain.JobStageConvertToMarkdown, Attempts: 1})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Status != domain.JobStatusPending {
		t.Fatalf("expected pending status, got %s", result.Status)
	}
	if !result.IncrementAttempt {
		t.Fatalf("expected attempts to be incremented")
	}
	if result.NextRun == nil {
		t.Fatal("expected next run to be scheduled")
	}

	expectedDelay := 200 * time.Millisecond * 2
	actualDelay := result.NextRun.Sub(start)
	if actualDelay < expectedDelay-50*time.Millisecond || actualDelay > expectedDelay+250*time.Millisecond {
		t.Fatalf("expected backoff around %s, got %s", expectedDelay, actualDelay)
	}
	if len(repo.statusUpdates) != 0 {
		t.Fatalf("document status should stay unchanged, got updates: %v", repo.statusUpdates)
	}
	if doc.Status == domain.DocumentStatusFailed {
		t.Fatalf("document should not be marked failed on temporary errors")
	}
}

func TestHandleConvertHonorsMaxAttemptsForTemporaryErrors(t *testing.T) {
	now := time.Now()
	doc := &domain.Document{
		ID:                "doc-max-attempts",
		Name:              "Doc",
		Owner:             "owner",
		Repository:        "repo",
		Branch:            "main",
		BaseBranch:        "main",
		Path:              "docs/doc.md",
		Status:            domain.DocumentStatusStoredOriginal,
		OriginalBucket:    "orig-bucket",
		OriginalObjectKey: "path/doc.docx",
		MarkdownBucket:    "md-bucket",
		MarkdownObjectKey: "path/doc.md",
		Versions:          []domain.DocumentVersion{{Version: 1, OriginalObjectKey: "path/doc.docx", MarkdownObjectKey: "path/doc.md"}},
	}

	repo := &publishRepoStub{doc: doc, now: now}
	transformer := &errorTransformer{err: status.Error(codes.Unavailable, "transformer unavailable")}
	activities := NewActivities(repo, transformer, nil)
	worker := NewStageWorker(repo, activities, domain.JobStageConvertToMarkdown, 1, time.Second)
	worker.maxAttempts = 1

	result, err := worker.handleConvert(context.Background(), domain.ProcessingJob{TaskID: "job-max", DocumentID: doc.ID, Stage: domain.JobStageConvertToMarkdown, Attempts: 1, Payload: map[string]any{}})
	if err == nil {
		t.Fatal("expected error after reaching max attempts")
	}
	if result.Payload["last_error"] == nil {
		t.Fatalf("expected last_error recorded, got %v", result.Payload)
	}
	if doc.Status != domain.DocumentStatusFailed {
		t.Fatalf("document should be marked failed after exhausting attempts, got %s", doc.Status)
	}
	if len(repo.statusUpdates) == 0 || repo.statusUpdates[len(repo.statusUpdates)-1] != domain.DocumentStatusFailed {
		t.Fatalf("expected failed status update, got %v", repo.statusUpdates)
	}
}

func waitUntil(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.After(time.Second)
	for {
		if cond() {
			return
		}
		select {
		case <-deadline:
			t.Fatal("condition not met in time")
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}
}
