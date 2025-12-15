package usecase

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/mono-repo/docling-transformer-service/internal/domain"
	transformations "github.com/mono-repo/docling-transformer-service/internal/domain/transformations"
	repo "github.com/mono-repo/docling-transformer-service/internal/repository"
	"github.com/mono-repo/docling-transformer-service/internal/usecase/metrics"
)

type pollingRepoStub struct {
	markWaitingCalls int
	updateInfoCalls  int
	markDoneCalls    int
	cleanupCalls     int
}

var _ repo.QueueRepository = (*pollingRepoStub)(nil)

func (p *pollingRepoStub) Enqueue(context.Context, transformations.EnqueueParams) (uuid.UUID, error) {
	return uuid.Nil, nil
}
func (p *pollingRepoStub) CountActive(context.Context) (int, error) { return 0, nil }
func (p *pollingRepoStub) AcquireNext(context.Context) (*transformations.TransformationTask, error) {
	return nil, nil
}
func (p *pollingRepoStub) AcquireBatch(context.Context, int) ([]*transformations.TransformationTask, error) {
	return nil, nil
}
func (p *pollingRepoStub) GetLatestByDocument(context.Context, string) (*transformations.TransformationTask, error) {
	return nil, nil
}
func (p *pollingRepoStub) GetByID(context.Context, uuid.UUID) (*transformations.TransformationTask, error) {
	return nil, nil
}
func (p *pollingRepoStub) MarkDone(context.Context, uuid.UUID, string, string) error {
	p.markDoneCalls++
	return nil
}
func (p *pollingRepoStub) MarkFailed(context.Context, uuid.UUID, string) error { return nil }
func (p *pollingRepoStub) UpdateDoclingInfo(context.Context, uuid.UUID, string, int) error {
	p.updateInfoCalls++
	return nil
}
func (p *pollingRepoStub) MarkDoclingWaiting(context.Context, uuid.UUID, string, int, time.Time) error {
	p.markWaitingCalls++
	return nil
}
func (p *pollingRepoStub) ActivateWaiting(context.Context, time.Time, time.Duration) (int, int, error) {
	return 0, 0, nil
}
func (p *pollingRepoStub) RestoreProcessing(context.Context) (int, error) { return 0, nil }
func (p *pollingRepoStub) RestoreStuckProcessing(context.Context, time.Duration, int, int) (int, int, error) {
	return 0, 0, nil
}
func (p *pollingRepoStub) RetryFailed(context.Context, time.Time, int) (int, int, error) {
	return 0, 0, nil
}
func (p *pollingRepoStub) CleanupFinished(context.Context, time.Duration) (int, error) {
	p.cleanupCalls++
	return 0, nil
}
func (p *pollingRepoStub) CountByStatus(context.Context) (int, int, int, error) { return 0, 0, 0, nil }

func TestConversionExecutorSetsWaitingState(t *testing.T) {
	repoStub := &pollingRepoStub{}
	storage := &trackingStorage{}
	conv := &submitOnlyConverter{jobID: "job-123"}
	poller := NewDoclingPoller(conv, repoStub, nil, nil, DoclingPollerConfig{})
	executor := NewConversionExecutor(repoStub, conv, storage, poller, nil, nil, ConversionExecutorConfig{MarkdownBucket: "md"})

	task := &transformations.TransformationTask{
		ID:                uuid.New(),
		OriginalBucket:    "orig",
		OriginalObjectKey: "path/file.docx",
	}

	state, err := executor.ProcessTask(context.Background(), task)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if state != StateWaitingDocling {
		t.Fatalf("expected waiting state, got %s", state)
	}

	if repoStub.markWaitingCalls != 1 {
		t.Fatalf("expected mark waiting to be called once, got %d", repoStub.markWaitingCalls)
	}
}

func TestConversionExecutorReschedulesAfterTemporarySubmitError(t *testing.T) {
	repoStub := &pollingRepoStub{}
	storage := &trackingStorage{}
	conv := &submitOnlyConverter{submitErr: &domain.TemporarySubmitRetryExhaustedError{Err: temporaryStatusError{}}}
	poller := NewDoclingPoller(conv, repoStub, nil, nil, DoclingPollerConfig{InitialInterval: time.Second})
	executor := NewConversionExecutor(repoStub, conv, storage, poller, nil, nil, ConversionExecutorConfig{MarkdownBucket: "md"})

	task := &transformations.TransformationTask{
		ID:                uuid.New(),
		OriginalBucket:    "orig",
		OriginalObjectKey: "path/file.docx",
		RetryCount:        2,
	}

	state, err := executor.ProcessTask(context.Background(), task)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if state != StateWaitingDocling {
		t.Fatalf("expected waiting state, got %s", state)
	}

	if repoStub.markWaitingCalls != 1 {
		t.Fatalf("expected mark waiting to be called once, got %d", repoStub.markWaitingCalls)
	}

	if task.NextPollAt == nil {
		t.Fatalf("expected next poll to be scheduled")
	}
}

func TestConversionExecutorDownloadsFromSourceURL(t *testing.T) {
	repoStub := &pollingRepoStub{}
	storage := &trackingStorage{}
	conv := &submitOnlyConverter{jobID: "job-123"}
	poller := NewDoclingPoller(conv, repoStub, nil, nil, DoclingPollerConfig{})
	httpClient := &recordingHTTPClient{resp: &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(bytes.NewBufferString("url-content"))}}

	executor := NewConversionExecutor(repoStub, conv, storage, poller, nil, nil, ConversionExecutorConfig{
		MarkdownBucket:       "md",
		FastOperationTimeout: time.Second,
		HTTPClient:           httpClient,
	})

	task := &transformations.TransformationTask{
		ID:                uuid.New(),
		SourceURL:         "https://example.com/doc.docx",
		OriginalObjectKey: "path/file.docx",
	}

	state, err := executor.ProcessTask(context.Background(), task)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if state != StateWaitingDocling {
		t.Fatalf("expected waiting state, got %s", state)
	}
	if len(httpClient.requests) != 1 {
		t.Fatalf("expected single http request, got %d", len(httpClient.requests))
	}
	if storage.downloadCalls != 0 {
		t.Fatalf("expected no storage downloads, got %d", storage.downloadCalls)
	}
	if string(conv.lastRequest.Content) != "url-content" {
		t.Fatalf("converter received wrong content: %s", string(conv.lastRequest.Content))
	}
}

func TestConversionExecutorDownloadsFromSourceBucket(t *testing.T) {
	repoStub := &pollingRepoStub{}
	storage := &trackingStorage{downloadContent: []byte("bucket-content")}
	conv := &submitOnlyConverter{jobID: "job-123"}
	poller := NewDoclingPoller(conv, repoStub, nil, nil, DoclingPollerConfig{})
	httpClient := &recordingHTTPClient{}

	executor := NewConversionExecutor(repoStub, conv, storage, poller, nil, nil, ConversionExecutorConfig{
		MarkdownBucket:       "md",
		FastOperationTimeout: time.Second,
		HTTPClient:           httpClient,
	})

	task := &transformations.TransformationTask{
		ID:              uuid.New(),
		SourceBucket:    "source-bucket",
		SourceObjectKey: "incoming/file.docx",
	}

	state, err := executor.ProcessTask(context.Background(), task)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if state != StateWaitingDocling {
		t.Fatalf("expected waiting state, got %s", state)
	}
	if storage.downloadCalls != 1 {
		t.Fatalf("expected single storage download, got %d", storage.downloadCalls)
	}
	if len(httpClient.requests) != 0 {
		t.Fatalf("expected no http requests, got %d", len(httpClient.requests))
	}
	if string(conv.lastRequest.Content) != "bucket-content" {
		t.Fatalf("converter received wrong content: %s", string(conv.lastRequest.Content))
	}
}

func TestConversionExecutorErrorsOnUnavailableSource(t *testing.T) {
	repoStub := &pollingRepoStub{}
	storage := &trackingStorage{downloadErr: errors.New("not found")}
	conv := &submitOnlyConverter{jobID: "job-123"}
	poller := NewDoclingPoller(conv, repoStub, nil, nil, DoclingPollerConfig{})
	httpClient := &recordingHTTPClient{err: errors.New("unreachable")}

	executor := NewConversionExecutor(repoStub, conv, storage, poller, nil, nil, ConversionExecutorConfig{
		MarkdownBucket:       "md",
		FastOperationTimeout: time.Second,
		HTTPClient:           httpClient,
	})

	_, err := executor.ProcessTask(context.Background(), &transformations.TransformationTask{ID: uuid.New(), SourceURL: "https://example.com/doc.docx"})
	if err == nil {
		t.Fatalf("expected error for unreachable url")
	}

	state, err := executor.ProcessTask(context.Background(), &transformations.TransformationTask{ID: uuid.New(), SourceBucket: "src", SourceObjectKey: "missing.docx"})
	if err == nil || state != StateProcessing {
		t.Fatalf("expected processing state with error for missing object, got state=%s err=%v", state, err)
	}
}

type temporaryStatusError struct{}

func (temporaryStatusError) Error() string   { return "temporary" }
func (temporaryStatusError) Temporary() bool { return true }
func (temporaryStatusError) StatusCode() int { return 503 }

func TestDoclingPollerTemporaryErrorReturnsWaiting(t *testing.T) {
	repoStub := &pollingRepoStub{}
	conv := &converterWithTemporaryError{}
	poller := NewDoclingPoller(conv, repoStub, nil, nil, DoclingPollerConfig{MaxAttempts: 3})

	task := &transformations.TransformationTask{ID: uuid.New(), DoclingJobID: "job-1", CreatedAt: time.Now().Add(-time.Minute)}

	_, err := poller.Poll(context.Background(), task)
	if !errors.Is(err, ErrDoclingWaiting) {
		t.Fatalf("expected waiting error, got %v", err)
	}

	if repoStub.markWaitingCalls != 1 {
		t.Fatalf("expected mark waiting call, got %d", repoStub.markWaitingCalls)
	}
	if repoStub.updateInfoCalls != 1 {
		t.Fatalf("expected update info call, got %d", repoStub.updateInfoCalls)
	}
}

type converterWithTemporaryError struct{}

func (c *converterWithTemporaryError) Submit(context.Context, domain.ConvertRequest) (string, error) {
	return "job", nil
}
func (c *converterWithTemporaryError) CheckStatus(context.Context, string) (domain.StatusCheckResult, error) {
	return domain.StatusCheckResult{}, temporaryStatusError{}
}

type staticResultConverter struct{}

func (c *staticResultConverter) Submit(context.Context, domain.ConvertRequest) (string, error) {
	return "", nil
}
func (c *staticResultConverter) CheckStatus(context.Context, string) (domain.StatusCheckResult, error) {
	return domain.StatusCheckResult{Status: "ok", Result: &domain.ConvertResult{Content: []byte("result"), MediaType: "text/md"}}, nil
}

type trackingStorage struct {
	uploadCalls     int
	downloadCalls   int
	downloadErr     error
	downloadContent []byte
}

func (s *trackingStorage) Download(context.Context, string, string) ([]byte, error) {
	s.downloadCalls++
	if s.downloadErr != nil {
		return nil, s.downloadErr
	}
	if s.downloadContent != nil {
		return s.downloadContent, nil
	}

	return []byte("original"), nil
}

func (s *trackingStorage) Upload(context.Context, string, string, string, []byte) error {
	s.uploadCalls++
	return nil
}

type recordingHTTPClient struct {
	requests []*http.Request
	resp     *http.Response
	err      error
}

func (c *recordingHTTPClient) Do(req *http.Request) (*http.Response, error) {
	c.requests = append(c.requests, req)
	if c.err != nil {
		return nil, c.err
	}
	if c.resp == nil {
		return &http.Response{StatusCode: http.StatusInternalServerError, Body: io.NopCloser(bytes.NewBufferString(""))}, nil
	}

	return c.resp, nil
}

type submitOnlyConverter struct {
	jobID        string
	submitCalled bool
	checkCalled  bool
	submitErr    error
	lastRequest  domain.ConvertRequest
}

func (c *submitOnlyConverter) Submit(_ context.Context, req domain.ConvertRequest) (string, error) {
	c.submitCalled = true
	c.lastRequest = req
	if c.submitErr != nil {
		return "", c.submitErr
	}
	if c.jobID == "" {
		c.jobID = "job-from-submit"
	}
	if req.ContentType == "" {
		return "", errors.New("content type is empty")
	}

	return c.jobID, nil
}

func (c *submitOnlyConverter) CheckStatus(context.Context, string) (domain.StatusCheckResult, error) {
	c.checkCalled = true
	return domain.StatusCheckResult{}, errors.New("status should not be called")
}

type queueProcessorStub struct {
	states []TaskState
	calls  int
}

func (p *queueProcessorStub) ProcessTask(context.Context, *transformations.TransformationTask) (TaskState, error) {
	defer func() { p.calls++ }()
	if p.calls < len(p.states) {
		return p.states[p.calls], nil
	}

	return StateCompleted, nil
}

type queueRepoStub struct {
	repo.QueueRepository
	batches [][]*transformations.TransformationTask
}

func (r *queueRepoStub) AcquireBatch(context.Context, int) ([]*transformations.TransformationTask, error) {
	if len(r.batches) == 0 {
		return nil, nil
	}

	batch := r.batches[0]
	r.batches = r.batches[1:]
	return batch, nil
}

type cleanupRepoStub struct {
	pollingRepoStub
	activated int
	retried   int
	exhausted int
}

func (r *cleanupRepoStub) ActivateWaiting(context.Context, time.Time, time.Duration) (int, int, error) {
	r.activated++
	return 1, 0, nil
}

func (r *cleanupRepoStub) RetryFailed(ctx context.Context, olderThan time.Time, maxRetries int) (int, int, error) {
	if olderThan.IsZero() || maxRetries <= 0 {
		return 0, 0, nil
	}

	r.retried = 1
	r.exhausted = 1
	return r.retried, r.exhausted, nil
}

func TestQueueWorkerHandlesWaitingAndCompletionStates(t *testing.T) {
	repoStub := &queueRepoStub{batches: [][]*transformations.TransformationTask{{{ID: uuid.New()}, {ID: uuid.New()}}}}
	worker := NewQueueWorker(repoStub, &queueProcessorStub{states: []TaskState{StateWaitingDocling, StateCompleted}}, metrics.NewWorkerMetrics(nil), nil, QueueWorkerConfig{Workers: 1, PollInterval: time.Millisecond})

	worker.handleSingle(context.Background(), &transformations.TransformationTask{ID: uuid.New()})
	worker.handleSingle(context.Background(), &transformations.TransformationTask{ID: uuid.New()})
}

func TestMaintenanceRunnerTriggersCleanup(t *testing.T) {
	repoStub := &cleanupRepoStub{}
	metrics := metrics.NewWorkerMetrics(nil)
	runner := NewMaintenanceRunner(repoStub, metrics, nil, MaintenanceConfig{Interval: time.Millisecond, CleanupRetention: time.Second})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var wg sync.WaitGroup
	runner.Start(ctx, &wg)
	time.Sleep(3 * time.Millisecond)
	cancel()
	wg.Wait()

	if repoStub.cleanupCalls == 0 {
		t.Fatalf("expected cleanup to be triggered")
	}
	if repoStub.activated == 0 {
		t.Fatalf("expected waiting activation to be triggered")
	}
}

func TestMaintenanceRunnerRetriesFailedTasks(t *testing.T) {
	repoStub := &cleanupRepoStub{}
	runner := NewMaintenanceRunner(repoStub, metrics.NewWorkerMetrics(nil), nil, MaintenanceConfig{
		Interval:         time.Millisecond,
		FailedRetryDelay: time.Minute,
		MaxFailedRetries: 3,
	})

	runner.runOnce(context.Background())

	if repoStub.retried == 0 || repoStub.exhausted == 0 {
		t.Fatalf("expected failed tasks retry to be triggered")
	}
}
