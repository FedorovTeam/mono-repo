package document

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/durationpb"

	"backend-document-service/internal/domain"
	githubpublisherv1 "backend-document-service/proto/backend/githubpublisher/v1"
	preprocessv1 "backend-document-service/proto/backend/preprocess/v1"
	tableextractorv1 "backend-document-service/proto/backend/tableextractor/v1"
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
	lastVersion    *domain.DocumentVersion
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
		r.lastVersion = version
		if version != nil {
			if version.MarkdownObjectKey != "" {
				r.doc.MarkdownObjectKey = version.MarkdownObjectKey
			}
			if version.OriginalObjectKey != "" {
				r.doc.OriginalObjectKey = version.OriginalObjectKey
			}
		}
	}
	r.statusUpdates = append(r.statusUpdates, to)
	r.lastUpdateArgs.reason = reason
	r.lastUpdateArgs.errorReason = errorReason
	return r.doc, nil
}

func (r *publishRepoStub) UpdateDocumentOptions(context.Context, string, domain.ConversionStrategy, *bool) (*domain.Document, error) {
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
	resp        *transformerv1.ConvertDocumentToMarkdownResponse
	lastRequest *transformerv1.ConvertDocumentToMarkdownRequest
}

func (s *staticTransformer) ConvertDocumentToMarkdown(ctx context.Context, in *transformerv1.ConvertDocumentToMarkdownRequest, opts ...grpc.CallOption) (*transformerv1.ConvertDocumentToMarkdownResponse, error) {
	s.lastRequest = in
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

type tableExtractorStub struct {
	resp        *tableextractorv1.ExtractTablesFromPdfResponse
	pollResp    *tableextractorv1.GetTableExtractionJobResponse
	err         error
	lastSubmit  *tableextractorv1.ExtractTablesFromPdfRequest
	lastPollReq *tableextractorv1.GetTableExtractionJobRequest
}

func (s *tableExtractorStub) ExtractTablesFromPdf(ctx context.Context, in *tableextractorv1.ExtractTablesFromPdfRequest, opts ...grpc.CallOption) (*tableextractorv1.ExtractTablesFromPdfResponse, error) {
	s.lastSubmit = in
	if s.err != nil {
		return nil, s.err
	}
	if s.resp != nil {
		return s.resp, nil
	}
	return &tableextractorv1.ExtractTablesFromPdfResponse{}, nil
}

func (s *tableExtractorStub) GetTableExtractionJob(ctx context.Context, in *tableextractorv1.GetTableExtractionJobRequest, opts ...grpc.CallOption) (*tableextractorv1.GetTableExtractionJobResponse, error) {
	s.lastPollReq = in
	if s.err != nil && s.resp == nil {
		return nil, s.err
	}
	if s.pollResp != nil {
		return s.pollResp, nil
	}
	return &tableextractorv1.GetTableExtractionJobResponse{}, nil
}

type staticPreprocess struct {
	submitResp *preprocessv1.SubmitPreprocessResponse
	statusResp *preprocessv1.GetPreprocessStatusResponse
	submitErr  error
	statusErr  error
	lastSubmit *preprocessv1.SubmitPreprocessRequest
	lastStatus *preprocessv1.GetPreprocessStatusRequest
}

func (s *staticPreprocess) SubmitPreprocess(ctx context.Context, in *preprocessv1.SubmitPreprocessRequest, opts ...grpc.CallOption) (*preprocessv1.SubmitPreprocessResponse, error) {
	s.lastSubmit = in
	if s.submitErr != nil {
		return nil, s.submitErr
	}
	return s.submitResp, nil
}

func (s *staticPreprocess) GetPreprocessStatus(ctx context.Context, in *preprocessv1.GetPreprocessStatusRequest, opts ...grpc.CallOption) (*preprocessv1.GetPreprocessStatusResponse, error) {
	s.lastStatus = in
	if s.statusErr != nil {
		return nil, s.statusErr
	}
	return s.statusResp, nil
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
	activities := NewActivities(repo, nil, nil, nil, publisher, nil)
	worker := NewStageWorker(repo, activities, domain.JobStagePublishToGitHub, 1, time.Second, nil)

	result, err := worker.handlePublish(context.Background(), domain.ProcessingJob{TaskID: "job-1", DocumentID: doc.ID, Stage: domain.JobStagePublishToGitHub, Payload: (domain.PublishPayload{
		CommitMessage: "commit",
		AuthorName:    "Author",
		AuthorEmail:   "author@example.com",
	}).ToMap()})
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
	activities := NewActivities(repo, nil, nil, nil, publisher, nil)
	worker := NewStageWorker(repo, activities, domain.JobStagePublishToGitHub, 1, time.Second, nil)

	start := time.Now()
	result, err := worker.handlePublish(context.Background(), domain.ProcessingJob{TaskID: "job-2", DocumentID: doc.ID, Stage: domain.JobStagePublishToGitHub, Payload: (domain.PublishPayload{
		CommitMessage: "commit",
		AuthorName:    "Author",
		AuthorEmail:   "author@example.com",
	}).ToMap()})
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
	if doc.Status != domain.DocumentStatusConvertedToMarkdown {
		t.Fatalf("expected document status to stay converted, got %s", doc.Status)
	}
	if len(repo.statusUpdates) != 0 {
		t.Fatalf("expected no document status updates, got %v", repo.statusUpdates)
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
				Payload:    (domain.PublishPayload{CommitMessage: "commit", AuthorName: "Author", AuthorEmail: "author@example.com"}).ToMap(),
				UpdatedAt:  fixed.Add(-time.Minute),
			},
		},
	}

	reconciler := NewProcessingReconciler(repo, 2, 30*time.Second, 15*time.Second)
	reconciler.now = func() time.Time { return repo.now }
	if err := reconciler.reconcile(context.Background(), time.Second); err != nil {
		t.Fatalf("unexpected reconcile error: %v", err)
	}

	recovered := repo.jobs["job-stuck"]
	if recovered.Status != domain.JobStatusPending {
		t.Fatalf("expected job to be re-queued, got %s", recovered.Status)
	}
	if len(repo.recoveryEvents) == 0 {
		t.Fatalf("expected recovery event to be recorded")
	}

	repo.now = fixed.Add(time.Minute)
	publisher := &staticPublisher{resp: &githubpublisherv1.PublishMarkdownResponse{Status: githubpublisherv1.PublishResultStatus_PUBLISH_RESULT_STATUS_SUCCESS, CommitSha: "commit-sha"}}
	activities := NewActivities(repo, nil, nil, nil, publisher, nil)
	worker := NewStageWorker(repo, activities, domain.JobStagePublishToGitHub, 2, time.Second, nil)

	ctx, cancel := context.WithCancel(context.Background())
	runErr := make(chan error, 1)
	go func() { runErr <- worker.Run(ctx, time.Second) }()

	waitUntil(t, func() bool {
		completed := repo.jobs["job-stuck"]
		return completed.Status == domain.JobStatusCompleted && doc.Status == domain.DocumentStatusPublishedToGitHub
	})
	cancel()
	if err := <-runErr; err != nil && !errors.Is(err, context.Canceled) {
		t.Fatalf("unexpected worker error: %v", err)
	}
}

func TestHandlePreprocessSubmitSchedulesRetry(t *testing.T) {
	now := time.Now()
	doc := &domain.Document{
		ID:                "doc-preprocess",
		Name:              "Doc.pdf",
		Owner:             "owner",
		Repository:        "repo",
		Branch:            "main",
		BaseBranch:        "main",
		Path:              "docs/doc.md",
		Status:            domain.DocumentStatusStoredOriginal,
		OriginalBucket:    "orig-bucket",
		OriginalObjectKey: "docs/doc.pdf",
		MarkdownBucket:    "md-bucket",
		MarkdownObjectKey: "docs/doc.md",
		Versions:          []domain.DocumentVersion{{Version: 1, OriginalObjectKey: "docs/doc.pdf", OriginalETag: "etag-original", MarkdownObjectKey: "docs/doc.md"}},
	}

	repo := &publishRepoStub{doc: doc, now: now}
	preprocess := &staticPreprocess{
		submitResp: &preprocessv1.SubmitPreprocessResponse{
			JobId:         "job-pre-1",
			Status:        preprocessv1.PreprocessStatus_PREPROCESS_STATUS_PENDING,
			CorrelationId: "corr-submit",
		},
	}
	activities := NewActivities(repo, preprocess, nil, nil, nil, nil)
	worker := NewStageWorker(repo, activities, domain.JobStagePreprocessToDocx, 1, 500*time.Millisecond, nil)

	result, err := worker.handlePreprocessToDocx(context.Background(), domain.ProcessingJob{TaskID: "job-pre", DocumentID: doc.ID, Stage: domain.JobStagePreprocessToDocx})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != domain.JobStatusPending {
		t.Fatalf("expected pending status, got %s", result.Status)
	}
	if result.NextRun == nil || result.NextRun.Sub(now) < 400*time.Millisecond {
		t.Fatalf("expected delayed retry, got %v", result.NextRun)
	}
	payload := domain.PreprocessPayloadFromMap(result.Payload)
	if payload.PreprocessJobID != "job-pre-1" {
		t.Fatalf("expected preprocess job id, got %+v", payload)
	}
	if payload.TargetObjectKey != "docs/doc.docx" {
		t.Fatalf("expected target object key to be docx, got %s", payload.TargetObjectKey)
	}
	if payload.PreprocessedObjectKey != payload.TargetObjectKey || payload.PreprocessedBucket != payload.TargetBucket {
		t.Fatalf("expected preprocessed location to match target, got %+v", payload)
	}
	if preprocess.lastSubmit == nil || preprocess.lastSubmit.CorrelationId != doc.ID {
		t.Fatalf("expected correlation id from document, got %+v", preprocess.lastSubmit)
	}
	if preprocess.lastSubmit.GetSourceEtag() != "etag-original" {
		t.Fatalf("expected source etag to be forwarded, got %s", preprocess.lastSubmit.GetSourceEtag())
	}
}

func TestHandlePreprocessCompletesAfterPoll(t *testing.T) {
	doc := &domain.Document{
		ID:                "doc-preprocess-complete",
		Name:              "Doc.pdf",
		Owner:             "owner",
		Repository:        "repo",
		Branch:            "main",
		BaseBranch:        "main",
		Path:              "docs/doc.md",
		Status:            domain.DocumentStatusStoredOriginal,
		OriginalBucket:    "orig-bucket",
		OriginalObjectKey: "docs/doc.pdf",
		MarkdownBucket:    "md-bucket",
		MarkdownObjectKey: "docs/doc.md",
		Versions:          []domain.DocumentVersion{{Version: 1, OriginalObjectKey: "docs/doc.pdf", OriginalETag: "etag-original", MarkdownObjectKey: "docs/doc.md"}},
	}
	repo := &publishRepoStub{doc: doc}
	preprocess := &staticPreprocess{
		statusResp: &preprocessv1.GetPreprocessStatusResponse{
			JobId:        "job-pre-2",
			Status:       preprocessv1.PreprocessStatus_PREPROCESS_STATUS_COMPLETED,
			TargetBucket: "processed",
			TargetKey:    "docs/doc.docx",
		},
	}
	activities := NewActivities(repo, preprocess, nil, nil, nil, nil)
	worker := NewStageWorker(repo, activities, domain.JobStagePreprocessToDocx, 1, time.Second, nil)

	payload := domain.PreprocessPayload{
		PreprocessJobID:   "job-pre-2",
		OriginalBucket:    doc.OriginalBucket,
		OriginalObjectKey: doc.OriginalObjectKey,
		TargetBucket:      "processed",
		TargetObjectKey:   "docs/doc.docx",
		PollAttempts:      2,
		JobPayloadMetadata: domain.JobPayloadMetadata{
			CorrelationID: "corr-2",
		},
	}.ToMap()

	result, err := worker.handlePreprocessToDocx(context.Background(), domain.ProcessingJob{
		TaskID:     "job-pre-2",
		DocumentID: doc.ID,
		Stage:      domain.JobStagePreprocessToDocx,
		Payload:    payload,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Event == nil || result.Event.EventType != "completed" {
		t.Fatalf("expected completed event, got %+v", result.Event)
	}
	updated := domain.PreprocessPayloadFromMap(result.Payload)
	if updated.PreprocessedBucket != "processed" || updated.PreprocessedObjectKey != "docs/doc.docx" {
		t.Fatalf("expected updated preprocessed location, got %+v", updated)
	}
	if updated.PollAttempts != 0 {
		t.Fatalf("expected poll attempts reset, got %d", updated.PollAttempts)
	}
}

func TestHandlePreprocessFailsOnFailedStatus(t *testing.T) {
	doc := &domain.Document{
		ID:                "doc-preprocess-failed",
		Name:              "Doc.pdf",
		Owner:             "owner",
		Repository:        "repo",
		Branch:            "main",
		BaseBranch:        "main",
		Path:              "docs/doc.md",
		Status:            domain.DocumentStatusStoredOriginal,
		OriginalBucket:    "orig-bucket",
		OriginalObjectKey: "docs/doc.pdf",
		MarkdownBucket:    "md-bucket",
		MarkdownObjectKey: "docs/doc.md",
		Versions:          []domain.DocumentVersion{{Version: 1, OriginalObjectKey: "docs/doc.pdf", MarkdownObjectKey: "docs/doc.md"}},
	}
	repo := &publishRepoStub{doc: doc}
	preprocess := &staticPreprocess{
		statusResp: &preprocessv1.GetPreprocessStatusResponse{
			JobId:        "job-pre-3",
			Status:       preprocessv1.PreprocessStatus_PREPROCESS_STATUS_FAILED,
			ErrorMessage: "convert failed",
			ErrorCode:    "FAILED",
		},
	}
	activities := NewActivities(repo, preprocess, nil, nil, nil, nil)
	worker := NewStageWorker(repo, activities, domain.JobStagePreprocessToDocx, 1, time.Second, nil)

	payload := domain.PreprocessPayload{
		PreprocessJobID:   "job-pre-3",
		OriginalBucket:    doc.OriginalBucket,
		OriginalObjectKey: doc.OriginalObjectKey,
		TargetBucket:      doc.OriginalBucket,
		TargetObjectKey:   "docs/doc.docx",
	}.ToMap()

	result, err := worker.handlePreprocessToDocx(context.Background(), domain.ProcessingJob{
		TaskID:     "job-pre-3",
		DocumentID: doc.ID,
		Stage:      domain.JobStagePreprocessToDocx,
		Payload:    payload,
	})
	if err == nil {
		t.Fatal("expected error for failed preprocess")
	}
	if result.Event == nil || result.Event.EventType != "failed" {
		t.Fatalf("expected failed event, got %+v", result.Event)
	}
	updated := domain.PreprocessPayloadFromMap(result.Payload)
	if updated.LastError == "" || updated.PreprocessStatus == "" {
		t.Fatalf("expected last error and status in payload, got %+v", updated)
	}
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
	activities := NewActivities(repo, nil, transformer, nil, nil, nil)
	worker := NewStageWorker(repo, activities, domain.JobStageConvertToMarkdown, 1, time.Second, nil)

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
	convertPayload := domain.ConvertPayloadFromMap(result.Payload)
	if convertPayload.QueueID != "queue-1" {
		t.Fatalf("expected queue_id in payload, got %v", result.Payload)
	}
	if convertPayload.Status != "pending" {
		t.Fatalf("expected transformer status in payload, got %v", result.Payload)
	}
}

func TestHandleConvertOmitsForceOCRFlag(t *testing.T) {
	doc := &domain.Document{
		ID:                "doc-ocr",
		Name:              "Doc.pdf",
		Owner:             "owner",
		Repository:        "repo",
		Branch:            "main",
		BaseBranch:        "main",
		Path:              "docs/doc.md",
		Status:            domain.DocumentStatusStoredOriginal,
		OriginalBucket:    "orig-bucket",
		OriginalObjectKey: "path/doc",
		MarkdownBucket:    "md-bucket",
		MarkdownObjectKey: "path/doc.md",
		Versions:          []domain.DocumentVersion{{Version: 1, OriginalObjectKey: "path/doc", MarkdownObjectKey: "path/doc.md"}},
	}

	repo := &publishRepoStub{doc: doc}
	transformer := &staticTransformer{resp: &transformerv1.ConvertDocumentToMarkdownResponse{QueueId: "queue-ocr", Status: "done", MarkdownObjectKey: "path/doc.md"}}
	activities := NewActivities(repo, nil, transformer, nil, nil, nil)
	worker := NewStageWorker(repo, activities, domain.JobStageConvertToMarkdown, 1, time.Second, nil)

	job := domain.ProcessingJob{TaskID: "job-ocr", DocumentID: doc.ID, Stage: domain.JobStageConvertToMarkdown, Payload: map[string]any{"force_ocr": true}}
	result, err := worker.handleConvert(context.Background(), job)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if transformer.lastRequest == nil {
		t.Fatalf("expected transformer request to be sent")
	}
	if transformer.lastRequest.GetFilenameExtension() != "pdf" {
		t.Fatalf("expected filename extension to be propagated, got %v", transformer.lastRequest.GetFilenameExtension())
	}

	payload := domain.ConvertPayloadFromMap(result.Payload)
	if payload.FilenameExtension != "pdf" {
		t.Fatalf("expected filename extension in payload, got %v", result.Payload)
	}
	if _, ok := result.Payload["force_ocr"]; ok {
		t.Fatalf("expected force_ocr to be omitted from payload, got %v", result.Payload)
	}
}

func TestHandleConvertUsesPreprocessedSourceWhenPresent(t *testing.T) {
	doc := &domain.Document{
		ID:                "doc-preprocessed",
		Name:              "Doc.pdf",
		Owner:             "owner",
		Repository:        "repo",
		Branch:            "main",
		BaseBranch:        "main",
		Path:              "docs/doc.md",
		Status:            domain.DocumentStatusStoredOriginal,
		OriginalBucket:    "orig-bucket",
		OriginalObjectKey: "docs/doc.pdf",
		MarkdownBucket:    "md-bucket",
		MarkdownObjectKey: "docs/doc.md",
		Versions:          []domain.DocumentVersion{{Version: 1, OriginalObjectKey: "docs/doc.pdf", MarkdownObjectKey: "docs/doc.md"}},
	}

	repo := &publishRepoStub{doc: doc}
	transformer := &staticTransformer{resp: &transformerv1.ConvertDocumentToMarkdownResponse{QueueId: "queue-prep", Status: "done", MarkdownObjectKey: "docs/doc.md"}}
	activities := NewActivities(repo, nil, transformer, nil, nil, nil)
	worker := NewStageWorker(repo, activities, domain.JobStageConvertToMarkdown, 1, time.Second, nil)

	payload := domain.ConvertPayload{
		PreprocessJobID:       "prep-123",
		PreprocessedBucket:    "prep-bucket",
		PreprocessedObjectKey: "docs/doc.docx",
		TargetObjectKey:       "docs/doc.docx",
		FilenameExtension:     "doc",
		OriginalBucket:        doc.OriginalBucket,
		OriginalObjectKey:     doc.OriginalObjectKey,
		MarkdownBucket:        doc.MarkdownBucket,
		MarkdownObjectKey:     doc.MarkdownObjectKey,
	}.ToMap()

	result, err := worker.handleConvert(context.Background(), domain.ProcessingJob{TaskID: "job-prep", DocumentID: doc.ID, Stage: domain.JobStageConvertToMarkdown, Payload: payload})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if transformer.lastRequest == nil {
		t.Fatalf("expected transformer request to be sent")
	}
	if transformer.lastRequest.GetOriginalBucket() != "prep-bucket" || transformer.lastRequest.GetOriginalObjectKey() != "docs/doc.docx" {
		t.Fatalf("expected preprocessed source to be used, got bucket=%s key=%s", transformer.lastRequest.GetOriginalBucket(), transformer.lastRequest.GetOriginalObjectKey())
	}
	if transformer.lastRequest.GetFilenameExtension() != "docx" {
		t.Fatalf("expected docx extension to be used, got %s", transformer.lastRequest.GetFilenameExtension())
	}
	if payload := domain.ConvertPayloadFromMap(result.Payload); payload.FilenameExtension != "docx" {
		t.Fatalf("expected filename extension to be overwritten to docx, got %s", payload.FilenameExtension)
	}

	if repo.lastVersion == nil || repo.lastVersion.OriginalObjectKey != "docs/doc.docx" {
		t.Fatalf("expected version to store preprocessed source, got %+v", repo.lastVersion)
	}
	if doc.OriginalObjectKey != "docs/doc.docx" {
		t.Fatalf("expected document original key to be updated, got %s", doc.OriginalObjectKey)
	}
	if repo.statusUpdates[len(repo.statusUpdates)-1] != domain.DocumentStatusConvertedToMarkdown {
		t.Fatalf("expected document status to be converted, got %v", repo.statusUpdates)
	}
	if result.Event == nil || result.Event.Payload == nil {
		t.Fatalf("expected event payload with source info, got %+v", result.Event)
	}
	eventPayload := result.Event.Payload
	if eventPayload["source_bucket"] != "prep-bucket" || eventPayload["source_object_key"] != "docs/doc.docx" {
		t.Fatalf("unexpected event payload: %+v", eventPayload)
	}
	if eventPayload["preprocess_job_id"] != "prep-123" {
		t.Fatalf("expected preprocess_job_id in event payload, got %+v", eventPayload)
	}
}

func TestHandleConvertKeepsDoclingPayloadWhenTablesDetected(t *testing.T) {
	doc := &domain.Document{
		ID:                 "doc-docling",
		Name:               "Doc.pdf",
		Owner:              "owner",
		Repository:         "repo",
		Branch:             "main",
		BaseBranch:         "main",
		Path:               "docs/doc.md",
		Status:             domain.DocumentStatusStoredOriginal,
		OriginalBucket:     "orig-bucket",
		OriginalObjectKey:  "docs/doc.pdf",
		MarkdownBucket:     "md-bucket",
		MarkdownObjectKey:  "docs/doc.md",
		ConversionStrategy: domain.ConversionStrategyDoclingWithTables,
		Versions:           []domain.DocumentVersion{{Version: 1, OriginalObjectKey: "docs/doc.pdf", MarkdownObjectKey: "docs/doc.md"}},
	}

	repo := &publishRepoStub{doc: doc}
	transformer := &staticTransformer{resp: &transformerv1.ConvertDocumentToMarkdownResponse{
		QueueId:           "queue-docling",
		Status:            "done",
		MarkdownObjectKey: "docs/doc.md",
		TableMetadata:     &transformerv1.TableMetadata{TablesCount: 2, Pages: []int32{1, 2}},
	}}
	worker := NewStageWorker(repo, NewActivities(repo, nil, transformer, nil, nil, nil), domain.JobStageConvertToMarkdown, 1, time.Second, nil)

	result, err := worker.handleConvert(context.Background(), domain.ProcessingJob{TaskID: "job-docling", DocumentID: doc.ID, Stage: domain.JobStageConvertToMarkdown})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(repo.statusUpdates) == 0 || repo.statusUpdates[len(repo.statusUpdates)-1] != domain.DocumentStatusConvertedToMarkdown {
		t.Fatalf("expected document to be marked converted, got %v", repo.statusUpdates)
	}
	payload := domain.ConvertPayloadFromMap(result.Payload)
	if payload.DoclingMarkdownObjectKey != "docs/doc.md" {
		t.Fatalf("expected docling markdown key in payload, got %s", payload.DoclingMarkdownObjectKey)
	}
	if payload.DoclingMarkdownBucket != "md-bucket" {
		t.Fatalf("expected docling markdown bucket to be kept, got %s", payload.DoclingMarkdownBucket)
	}
	if payload.TableCount != 2 {
		t.Fatalf("expected table count propagated, got %d", payload.TableCount)
	}
	if !reflect.DeepEqual(payload.TablePages, []int32{1, 2}) {
		t.Fatalf("expected table pages propagated, got %v", payload.TablePages)
	}
	if payload.ExtractTables {
		t.Fatalf("expected extract_tables to stay false; router decides based on detection data")
	}
	if payload.ConversionStrategy != domain.ConversionStrategyDoclingWithTables {
		t.Fatalf("expected conversion strategy to stay docling_with_tables, got %s", payload.ConversionStrategy)
	}
	if result.Event == nil || result.Event.EventType != "completed" {
		t.Fatalf("expected completed event without status change, got %+v", result.Event)
	}
}

func TestHandleConvertMarksDoclingWithoutDetectedTables(t *testing.T) {
	doc := &domain.Document{
		ID:                 "doc-docling-no-tables",
		Name:               "Doc.pdf",
		Owner:              "owner",
		Repository:         "repo",
		Branch:             "main",
		BaseBranch:         "main",
		Path:               "docs/doc.md",
		Status:             domain.DocumentStatusStoredOriginal,
		OriginalBucket:     "orig-bucket",
		OriginalObjectKey:  "docs/doc.pdf",
		MarkdownBucket:     "md-bucket",
		MarkdownObjectKey:  "docs/doc.md",
		ConversionStrategy: domain.ConversionStrategyDoclingWithTables,
		Versions:           []domain.DocumentVersion{{Version: 1, OriginalObjectKey: "docs/doc.pdf", MarkdownObjectKey: "docs/doc.md"}},
	}

	repo := &publishRepoStub{doc: doc}
	transformer := &staticTransformer{resp: &transformerv1.ConvertDocumentToMarkdownResponse{QueueId: "queue-docling", Status: "done", MarkdownObjectKey: "docs/doc.md"}}
	worker := NewStageWorker(repo, NewActivities(repo, nil, transformer, nil, nil, nil), domain.JobStageConvertToMarkdown, 1, time.Second, nil)

	result, err := worker.handleConvert(context.Background(), domain.ProcessingJob{TaskID: "job-docling", DocumentID: doc.ID, Stage: domain.JobStageConvertToMarkdown})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(repo.statusUpdates) == 0 || repo.statusUpdates[len(repo.statusUpdates)-1] != domain.DocumentStatusConvertedToMarkdown {
		t.Fatalf("expected document to be marked converted, got %v", repo.statusUpdates)
	}
	payload := domain.ConvertPayloadFromMap(result.Payload)
	if payload.ExtractTables {
		t.Fatalf("expected extract_tables to remain false without detected tables")
	}
	if payload.TableCount != 0 {
		t.Fatalf("expected table count to stay zero when none are detected, got %d", payload.TableCount)
	}
	if len(payload.TablePages) != 0 {
		t.Fatalf("expected no table pages when none are detected, got %v", payload.TablePages)
	}
	if result.Event == nil || result.Event.EventType != "completed" {
		t.Fatalf("expected completed event, got %+v", result.Event)
	}
}

func TestHandleConvertClearsStaleTableCountsWhenNoTablesReturned(t *testing.T) {
	doc := &domain.Document{
		ID:                "doc-stale-tables",
		Name:              "Doc.pdf",
		Owner:             "owner",
		Repository:        "repo",
		Branch:            "main",
		BaseBranch:        "main",
		Path:              "docs/doc.md",
		Status:            domain.DocumentStatusStoredOriginal,
		OriginalBucket:    "orig-bucket",
		OriginalObjectKey: "docs/doc.pdf",
		MarkdownBucket:    "md-bucket",
		MarkdownObjectKey: "docs/doc.md",
		Versions:          []domain.DocumentVersion{{Version: 1, OriginalObjectKey: "docs/doc.pdf", MarkdownObjectKey: "docs/doc.md"}},
	}

	repo := &publishRepoStub{doc: doc}
	transformer := &staticTransformer{resp: &transformerv1.ConvertDocumentToMarkdownResponse{
		QueueId:           "queue-docling",
		Status:            "done",
		MarkdownObjectKey: "docs/doc.md",
	}}
	router := domain.NewStageRouter(domain.RouterRules{TableExtractorEnabled: true, TableDetectionMinCount: 2})
	worker := NewStageWorker(repo, NewActivities(repo, nil, transformer, nil, nil, nil), domain.JobStageConvertToMarkdown, 1, time.Second, router)

	jobPayload := (domain.ConvertPayload{
		MarkdownBucket:    "md-bucket",
		MarkdownObjectKey: "docs/doc.md",
		TableCount:        3,
		TablePages:        []int32{1, 2},
	}).ToMap()

	result, err := worker.handleConvert(context.Background(), domain.ProcessingJob{TaskID: "job-docling", DocumentID: doc.ID, Stage: domain.JobStageConvertToMarkdown, Payload: jobPayload})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(repo.statusUpdates) == 0 || repo.statusUpdates[len(repo.statusUpdates)-1] != domain.DocumentStatusConvertedToMarkdown {
		t.Fatalf("expected document to be marked converted, got %v", repo.statusUpdates)
	}
	payload := domain.ConvertPayloadFromMap(result.Payload)
	if payload.TableCount != 0 {
		t.Fatalf("expected table count to be reset, got %d", payload.TableCount)
	}
	if len(payload.TablePages) != 0 {
		t.Fatalf("expected table pages to be cleared, got %v", payload.TablePages)
	}
	if payload.ExtractTables {
		t.Fatalf("expected extract_tables to stay disabled without detected tables")
	}
}

func TestHandleConvertEnablesTablesWhenMetadataArrivesWithoutStrategy(t *testing.T) {
	doc := &domain.Document{
		ID:                "doc-detected",
		Name:              "Doc.pdf",
		Owner:             "owner",
		Repository:        "repo",
		Branch:            "main",
		BaseBranch:        "main",
		Path:              "docs/doc.md",
		Status:            domain.DocumentStatusStoredOriginal,
		OriginalBucket:    "orig-bucket",
		OriginalObjectKey: "docs/doc.pdf",
		MarkdownBucket:    "md-bucket",
		MarkdownObjectKey: "docs/doc.md",
		Versions:          []domain.DocumentVersion{{Version: 1, OriginalObjectKey: "docs/doc.pdf", MarkdownObjectKey: "docs/doc.md"}},
	}

	repo := &publishRepoStub{doc: doc}
	transformer := &staticTransformer{resp: &transformerv1.ConvertDocumentToMarkdownResponse{
		QueueId:           "queue-detected",
		Status:            "done",
		MarkdownObjectKey: "docs/doc.md",
		TableCount:        3,
		TablePages:        []int32{1, 2, 3},
	}}
	worker := NewStageWorker(repo, NewActivities(repo, nil, transformer, nil, nil, nil), domain.JobStageConvertToMarkdown, 1, time.Second, nil)

	result, err := worker.handleConvert(context.Background(), domain.ProcessingJob{TaskID: "job-detected", DocumentID: doc.ID, Stage: domain.JobStageConvertToMarkdown})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(repo.statusUpdates) != 0 {
		t.Fatalf("expected document status to stay unchanged before table extraction, got %v", repo.statusUpdates)
	}
	payload := domain.ConvertPayloadFromMap(result.Payload)
	if payload.TableCount != 3 {
		t.Fatalf("expected table count propagated, got %d", payload.TableCount)
	}
	if !reflect.DeepEqual(payload.TablePages, []int32{1, 2, 3}) {
		t.Fatalf("expected table pages propagated, got %v", payload.TablePages)
	}
	if !payload.ExtractTables {
		t.Fatalf("expected extract_tables to be enabled when tables are detected without strategy")
	}
	if result.Event == nil || result.Event.EventType != "completed" {
		t.Fatalf("expected completed event, got %+v", result.Event)
	}
}

func TestHandleConvertMarksFailureWithoutDocumentStatusChange(t *testing.T) {
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
			Payload:    (domain.ConvertPayload{OriginalBucket: doc.OriginalBucket, OriginalObjectKey: doc.OriginalObjectKey, MarkdownBucket: doc.MarkdownBucket, MarkdownObjectKey: doc.MarkdownObjectKey}).ToMap(),
		},
	}}
	transformer := &staticTransformer{resp: &transformerv1.ConvertDocumentToMarkdownResponse{QueueId: "queue-2", Status: "failed"}}
	activities := NewActivities(repo, nil, transformer, nil, nil, nil)
	worker := NewStageWorker(repo, activities, domain.JobStageConvertToMarkdown, 1, time.Second, nil)

	ctx, cancel := context.WithCancel(context.Background())
	runErr := make(chan error, 1)
	go func() { runErr <- worker.Run(ctx, 10*time.Millisecond) }()
	waitUntil(t, func() bool { return repo.lastStatus == domain.JobStatusFailed })
	cancel()

	if err := <-runErr; err != nil && !errors.Is(err, context.Canceled) {
		t.Fatalf("unexpected worker error: %v", err)
	}

	if payload := domain.ConvertPayloadFromMap(repo.lastPayload); payload.LastError == "" {
		t.Fatalf("expected last_error to be stored in payload, got %v", repo.lastPayload)
	}
	if doc.Status != domain.DocumentStatusStoredOriginal {
		t.Fatalf("expected document status to remain unchanged, got %s", doc.Status)
	}
	if len(repo.statusUpdates) != 0 {
		t.Fatalf("expected no document status updates, got %v", repo.statusUpdates)
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
	activities := NewActivities(repo, nil, transformer, nil, nil, nil)
	worker := NewStageWorker(repo, activities, domain.JobStageConvertToMarkdown, 1, time.Second, nil)

	result, err := worker.handleConvert(context.Background(), domain.ProcessingJob{TaskID: "job-exhausted", DocumentID: doc.ID, Stage: domain.JobStageConvertToMarkdown, Payload: (domain.ConvertPayload{}).ToMap()})
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
	if payload := domain.ConvertPayloadFromMap(result.Payload); payload.LastError == "" {
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
	activities := NewActivities(repo, nil, transformer, nil, nil, nil)
	worker := NewStageWorker(repo, activities, domain.JobStageConvertToMarkdown, 1, 200*time.Millisecond, nil)

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
	activities := NewActivities(repo, nil, transformer, nil, nil, nil)
	worker := NewStageWorker(repo, activities, domain.JobStageConvertToMarkdown, 1, time.Second, nil)
	worker.maxAttempts = 1

	result, err := worker.handleConvert(context.Background(), domain.ProcessingJob{TaskID: "job-max", DocumentID: doc.ID, Stage: domain.JobStageConvertToMarkdown, Attempts: 1, Payload: (domain.ConvertPayload{}).ToMap()})
	if err == nil {
		t.Fatal("expected error after reaching max attempts")
	}
	if payload := domain.ConvertPayloadFromMap(result.Payload); payload.LastError == "" {
		t.Fatalf("expected last_error recorded, got %v", result.Payload)
	}
	if doc.Status != domain.DocumentStatusFailed {
		t.Fatalf("document should be marked failed after exhausting attempts, got %s", doc.Status)
	}
	if len(repo.statusUpdates) == 0 || repo.statusUpdates[len(repo.statusUpdates)-1] != domain.DocumentStatusFailed {
		t.Fatalf("expected failed status update, got %v", repo.statusUpdates)
	}
}

func TestHandleExtractTablesSubmitsAndPostpones(t *testing.T) {
	doc := &domain.Document{
		ID:                "doc-1",
		Status:            domain.DocumentStatusStoredOriginal,
		OriginalBucket:    "orig-bucket",
		OriginalObjectKey: "docs/input.pdf",
		MarkdownBucket:    "md-bucket",
		MarkdownObjectKey: "md/output.md",
		Versions:          []domain.DocumentVersion{{Version: 1}},
	}
	repo := &publishRepoStub{doc: doc}
	tableExtractor := &tableExtractorStub{
		resp: &tableextractorv1.ExtractTablesFromPdfResponse{
			Job: &tableextractorv1.TableExtractionJob{
				QueueId: "queue-1",
				Status:  tableextractorv1.TableExtractionStatus_TABLE_EXTRACTION_STATUS_PENDING,
				Artifacts: &tableextractorv1.TableArtifacts{
					Markdown: &tableextractorv1.ArtifactLocation{Bucket: "md-bucket", ObjectKey: "md/output/tables.md"},
				},
			},
		},
	}
	worker := NewStageWorker(repo, NewActivities(repo, nil, nil, tableExtractor, nil, nil), domain.JobStageExtractTables, 1, time.Second, nil)
	result, err := worker.handleJob(context.Background(), domain.ProcessingJob{DocumentID: doc.ID, Stage: domain.JobStageExtractTables, Payload: map[string]any{}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != domain.JobStatusPending {
		t.Fatalf("expected pending status, got %s", result.Status)
	}
	if result.NextRun == nil {
		t.Fatal("expected next run to be scheduled")
	}
	payload := domain.ExtractTablesPayloadFromMap(result.Payload)
	if payload.QueueID != "queue-1" {
		t.Fatalf("expected queue_id propagated, got %s", payload.QueueID)
	}
	if payload.ArtifactsBucket != "md-bucket" {
		t.Fatalf("expected artifacts bucket, got %s", payload.ArtifactsBucket)
	}
	if payload.MarkdownObjectKey == "" {
		t.Fatal("expected markdown object key to be filled")
	}
	if tableExtractor.lastSubmit == nil {
		t.Fatal("expected extract tables request to be sent")
	}
	if tableExtractor.lastSubmit.GetMarkdownBucket() != doc.MarkdownBucket {
		t.Fatalf("expected markdown bucket in request, got %s", tableExtractor.lastSubmit.GetMarkdownBucket())
	}
	if tableExtractor.lastSubmit.GetMarkdownObjectKey() != doc.MarkdownObjectKey {
		t.Fatalf("expected markdown key in request, got %s", tableExtractor.lastSubmit.GetMarkdownObjectKey())
	}
	if prefix := strings.TrimSpace(tableExtractor.lastSubmit.GetArtifactsPrefix()); prefix == "" || prefix != tablesArtifactsPrefix(doc.MarkdownObjectKey) {
		t.Fatalf("expected artifacts prefix from markdown key, got %q", prefix)
	}
}

func TestHandleExtractTablesMarksConverted(t *testing.T) {
	doc := &domain.Document{
		ID:                "doc-1",
		Status:            domain.DocumentStatusStoredOriginal,
		OriginalBucket:    "orig-bucket",
		OriginalObjectKey: "docs/input.pdf",
		MarkdownBucket:    "md-bucket",
		MarkdownObjectKey: "md/output.md",
		Versions:          []domain.DocumentVersion{{Version: 1}},
	}
	repo := &publishRepoStub{doc: doc}
	tableExtractor := &tableExtractorStub{
		pollResp: &tableextractorv1.GetTableExtractionJobResponse{
			Job: &tableextractorv1.TableExtractionJob{
				QueueId: "queue-1",
				Status:  tableextractorv1.TableExtractionStatus_TABLE_EXTRACTION_STATUS_DONE,
				Artifacts: &tableextractorv1.TableArtifacts{
					Markdown: &tableextractorv1.ArtifactLocation{Bucket: "md-bucket", ObjectKey: "md/output/tables.md"},
				},
				CorrelationId: "corr-1",
			},
		},
	}
	storage := &stubStorageClient{
		objects: map[string]map[string][]byte{
			"md-bucket": {
				"md/output.md":        []byte("# Base"),
				"md/output/tables.md": []byte("|h|"),
			},
		},
	}
	payload := (domain.ExtractTablesPayload{QueueID: "queue-1"}).ToMap()
	worker := NewStageWorker(repo, NewActivities(repo, nil, nil, tableExtractor, nil, storage), domain.JobStageExtractTables, 1, time.Second, nil)
	result, err := worker.handleJob(context.Background(), domain.ProcessingJob{DocumentID: doc.ID, Stage: domain.JobStageExtractTables, Payload: payload})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Event == nil || result.Event.EventType != "completed" {
		t.Fatalf("expected completed event, got %+v", result.Event)
	}
	if repo.doc.Status != domain.DocumentStatusConvertedToMarkdown {
		t.Fatalf("expected document converted status, got %s", repo.doc.Status)
	}
	if repo.lastVersion == nil || repo.lastVersion.MarkdownObjectKey != doc.MarkdownObjectKey {
		t.Fatalf("expected markdown key propagated, got %+v", repo.lastVersion)
	}
}

func TestHandleExtractTablesMergesDoclingMarkdownWithTables(t *testing.T) {
	doc := &domain.Document{
		ID:                "doc-merge",
		Status:            domain.DocumentStatusStoredOriginal,
		OriginalBucket:    "orig-bucket",
		OriginalObjectKey: "docs/input.pdf",
		MarkdownBucket:    "md-bucket",
		MarkdownObjectKey: "docs/output.md",
		Versions:          []domain.DocumentVersion{{Version: 1}},
	}
	repo := &publishRepoStub{doc: doc}
	baseMarkdown := "# Docling\n\n<!-- tables:start -->\nold\n<!-- tables:end -->"
	tablesMarkdown := "|t|"
	tableExtractor := &tableExtractorStub{
		pollResp: &tableextractorv1.GetTableExtractionJobResponse{
			Job: &tableextractorv1.TableExtractionJob{
				QueueId: "queue-merge",
				Status:  tableextractorv1.TableExtractionStatus_TABLE_EXTRACTION_STATUS_DONE,
				Artifacts: &tableextractorv1.TableArtifacts{
					Markdown: &tableextractorv1.ArtifactLocation{Bucket: "artifacts-bucket", ObjectKey: "tables/doc-merge/tables.md"},
				},
			},
		},
	}
	storage := &stubStorageClient{
		objects: map[string]map[string][]byte{
			"md-bucket": {
				"docs/output.md": []byte(baseMarkdown),
			},
			"artifacts-bucket": {
				"tables/doc-merge/tables.md": []byte(tablesMarkdown),
			},
		},
	}
	payload := (domain.ExtractTablesPayload{
		TableQueueID:             "queue-merge",
		ArtifactsBucket:          "artifacts-bucket",
		MarkdownBucket:           doc.MarkdownBucket,
		MarkdownObjectKey:        doc.MarkdownObjectKey,
		DoclingMarkdownBucket:    doc.MarkdownBucket,
		DoclingMarkdownObjectKey: doc.MarkdownObjectKey,
	}).ToMap()
	worker := NewStageWorker(repo, NewActivities(repo, nil, nil, tableExtractor, nil, storage), domain.JobStageExtractTables, 1, time.Second, nil)
	result, err := worker.handleJob(context.Background(), domain.ProcessingJob{DocumentID: doc.ID, Stage: domain.JobStageExtractTables, Payload: payload})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Event == nil || result.Event.EventType != "completed" {
		t.Fatalf("expected completed event, got %+v", result.Event)
	}
	if repo.doc.Status != domain.DocumentStatusConvertedToMarkdown {
		t.Fatalf("expected document converted status, got %s", repo.doc.Status)
	}
	if repo.lastVersion == nil || repo.lastVersion.MarkdownObjectKey == "" {
		t.Fatalf("expected markdown version to be recorded, got %+v", repo.lastVersion)
	}
	uploaded := storage.objects["markdown-bucket"][repo.lastVersion.MarkdownObjectKey]
	expectedMerged := mergeMarkdownWithTables(baseMarkdown, tablesMarkdown)
	if string(uploaded) != expectedMerged {
		t.Fatalf("expected merged markdown to be uploaded, got %q", string(uploaded))
	}
	if repo.doc.MarkdownObjectKey != repo.lastVersion.MarkdownObjectKey {
		t.Fatalf("expected document to store final markdown key, got %s vs %s", repo.doc.MarkdownObjectKey, repo.lastVersion.MarkdownObjectKey)
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
