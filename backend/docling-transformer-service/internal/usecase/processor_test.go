package usecase

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/mono-repo/docling-transformer-service/internal/domain"
	transformations "github.com/mono-repo/docling-transformer-service/internal/domain/transformations"
	"github.com/mono-repo/docling-transformer-service/internal/infrastructure/converter"
	repo "github.com/mono-repo/docling-transformer-service/internal/repository"
	"github.com/mono-repo/docling-transformer-service/internal/usecase/metrics"
)

type pollingRepoStub struct {
	markWaitingCalls    int
	updateInfoCalls     int
	markDoneCalls       int
	cleanupCalls        int
	lastWaitingNextPoll time.Time
}

var _ repo.QueueRepository = (*pollingRepoStub)(nil)

func (p *pollingRepoStub) Enqueue(context.Context, transformations.EnqueueParams) (uuid.UUID, error) {
	return uuid.Nil, nil
}
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
func (p *pollingRepoStub) MarkDoclingWaiting(ctx context.Context, taskID uuid.UUID, jobID string, pollCount int, nextPoll time.Time) error {
	p.markWaitingCalls++
	p.lastWaitingNextPoll = nextPoll
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

func TestConversionExecutorUsesSourceAsyncWhenSourceObjectProvided(t *testing.T) {
	repoStub := &pollingRepoStub{}
	storage := &trackingStorage{downloadContent: []byte("should-not-be-used")}
	conv := &submitOnlyConverter{jobID: "job-123"}
	poller := NewDoclingPoller(conv, repoStub, nil, nil, DoclingPollerConfig{})

	executor := NewConversionExecutor(repoStub, conv, storage, poller, nil, nil, ConversionExecutorConfig{
		MarkdownBucket: "md",
	})

	task := &transformations.TransformationTask{
		ID:                uuid.New(),
		OriginalBucket:    "orig",
		OriginalObjectKey: "path/file.docx",
		SourceBucket:      "source-bucket",
		SourceObjectKey:   "incoming/file.docx",
	}

	state, err := executor.ProcessTask(context.Background(), task)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if state != StateWaitingDocling {
		t.Fatalf("expected waiting state, got %s", state)
	}
	if storage.downloadCalls != 0 {
		t.Fatalf("expected no storage downloads, got %d", storage.downloadCalls)
	}
	if len(conv.lastRequest.Content) != 0 {
		t.Fatalf("expected no inline content, got %d bytes", len(conv.lastRequest.Content))
	}
	if conv.lastRequest.SourceBucket != "source-bucket" || conv.lastRequest.SourceObjectKey != "incoming/file.docx" {
		t.Fatalf("converter received wrong source object: %s/%s", conv.lastRequest.SourceBucket, conv.lastRequest.SourceObjectKey)
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

func TestDoclingPollerTreats404AsTemporary(t *testing.T) {
	repoStub := &pollingRepoStub{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/v1/status/poll/"):
			w.Header().Set("Content-Type", "application/json")
			http.Error(w, "not found", http.StatusNotFound)
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	conv := converter.NewDoclingConverter(converter.Config{Endpoint: server.URL}, nil)
	config := DoclingPollerConfig{InitialInterval: 10 * time.Millisecond, MaxInterval: time.Second, BackoffFactor: 2, MaxAttempts: 5}
	poller := NewDoclingPoller(conv, repoStub, nil, nil, config)

	task := &transformations.TransformationTask{ID: uuid.New(), DoclingJobID: "job-404", CreatedAt: time.Now().Add(-time.Minute)}

	result, err := poller.Poll(context.Background(), task)
	if !errors.Is(err, ErrDoclingWaiting) {
		t.Fatalf("expected waiting error, got %v", err)
	}
	if result != nil {
		t.Fatalf("expected no result on temporary error")
	}

	if repoStub.markWaitingCalls != 1 {
		t.Fatalf("expected mark waiting call, got %d", repoStub.markWaitingCalls)
	}
	if repoStub.updateInfoCalls != 1 {
		t.Fatalf("expected update info call, got %d", repoStub.updateInfoCalls)
	}

	if task.NextPollAt == nil || task.NextPollAt.IsZero() {
		t.Fatalf("expected next poll to be scheduled, got %v", task.NextPollAt)
	}
	if repoStub.lastWaitingNextPoll.IsZero() {
		t.Fatalf("expected next poll stored in repo, got zero time")
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

type sequentialQueueRepoStub struct {
	pollingRepoStub
	tasks         []*transformations.TransformationTask
	acquireLimits []int
	waiting       atomic.Bool
	mu            sync.Mutex
}

func (r *sequentialQueueRepoStub) AcquireBatch(_ context.Context, limit int) ([]*transformations.TransformationTask, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.acquireLimits = append(r.acquireLimits, limit)

	if r.waiting.Load() || len(r.tasks) == 0 {
		return nil, nil
	}

	if limit > len(r.tasks) {
		limit = len(r.tasks)
	}

	batch := r.tasks[:limit]
	r.tasks = r.tasks[limit:]
	return batch, nil
}

func (r *sequentialQueueRepoStub) SetWaiting(waiting bool) {
	r.waiting.Store(waiting)
}

type waitingProcessorStub struct {
	repo      *sequentialQueueRepoStub
	states    []TaskState
	calls     atomic.Int64
	processed chan uuid.UUID
}

func (p *waitingProcessorStub) ProcessTask(_ context.Context, task *transformations.TransformationTask) (TaskState, error) {
	callIdx := int(p.calls.Load())
	p.calls.Add(1)

	state := TaskState(StateCompleted)
	if callIdx < len(p.states) {
		state = p.states[callIdx]
	}

	p.repo.SetWaiting(state == StateWaitingDocling)

	select {
	case p.processed <- task.ID:
	default:
	}

	return state, nil
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

func TestQueueWorkerSequentialProcessingWithBatchSizeOne(t *testing.T) {
	taskOne := &transformations.TransformationTask{ID: uuid.New()}
	taskTwo := &transformations.TransformationTask{ID: uuid.New()}

	repoStub := &sequentialQueueRepoStub{tasks: []*transformations.TransformationTask{taskOne, taskTwo}}
	processor := &waitingProcessorStub{
		repo:      repoStub,
		states:    []TaskState{StateWaitingDocling, StateCompleted},
		processed: make(chan uuid.UUID, 2),
	}

	worker := NewQueueWorker(repoStub, processor, metrics.NewWorkerMetrics(nil), nil, QueueWorkerConfig{
		Workers:      1,
		PollInterval: 5 * time.Millisecond,
		BatchSize:    1,
		RetryBackoff: time.Millisecond,
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var wg sync.WaitGroup
	worker.Start(ctx, &wg)

	select {
	case id := <-processor.processed:
		if id != taskOne.ID {
			t.Fatalf("expected first processed task to be %s, got %s", taskOne.ID, id)
		}
	case <-time.After(time.Second):
		t.Fatalf("timeout waiting for first task")
	}

	time.Sleep(20 * time.Millisecond)

	if processor.calls.Load() != 1 {
		t.Fatalf("expected only one task to be processed while waiting, got %d", processor.calls.Load())
	}

	repoStub.SetWaiting(false)

	select {
	case id := <-processor.processed:
		if id != taskTwo.ID {
			t.Fatalf("expected second processed task to be %s, got %s", taskTwo.ID, id)
		}
	case <-time.After(time.Second):
		t.Fatalf("timeout waiting for second task")
	}

	cancel()
	wg.Wait()

	for _, limit := range repoStub.acquireLimits {
		if limit != 1 {
			t.Fatalf("expected AcquireBatch to be called with limit 1, got %d", limit)
		}
	}
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

func TestNormalizeConfigAppliesDefaults(t *testing.T) {
	cfg := Config{}

	normalized := normalizeConfig(cfg)

	if normalized.MaxWorkers != 1 {
		t.Fatalf("expected default max workers to be 1, got %d", normalized.MaxWorkers)
	}
	if normalized.PollInterval != time.Second {
		t.Fatalf("expected default poll interval to be 1s, got %s", normalized.PollInterval)
	}
	if normalized.BatchSize != 1 {
		t.Fatalf("expected default batch size to be 1, got %d", normalized.BatchSize)
	}
	if normalized.RetryBackoff != normalized.PollInterval {
		t.Fatalf("expected retry backoff to equal poll interval, got %s", normalized.RetryBackoff)
	}
	if normalized.CleanupInterval != 30*time.Second {
		t.Fatalf("expected default cleanup interval to be 30s, got %s", normalized.CleanupInterval)
	}
	if normalized.DoclingInitialInterval != 15*time.Second {
		t.Fatalf("expected default docling initial interval to be 15s, got %s", normalized.DoclingInitialInterval)
	}
	if normalized.DoclingBackoffFactor != 2 {
		t.Fatalf("expected default docling backoff factor to be 2, got %f", normalized.DoclingBackoffFactor)
	}
	if normalized.DoclingMaxInterval != 30*time.Minute {
		t.Fatalf("expected default docling max interval to be 30m, got %s", normalized.DoclingMaxInterval)
	}
}

func TestNormalizeConfigKeepsProvidedValues(t *testing.T) {
	cfg := Config{
		MarkdownBucket:         "custom",
		MaxWorkers:             5,
		PollInterval:           2 * time.Second,
		BatchSize:              10,
		RetryBackoff:           3 * time.Second,
		CleanupInterval:        time.Minute,
		DoclingInitialInterval: 5 * time.Second,
		DoclingBackoffFactor:   1.5,
		DoclingMaxInterval:     10 * time.Minute,
	}

	normalized := normalizeConfig(cfg)

	if normalized != cfg {
		t.Fatalf("expected custom config to remain unchanged after normalization")
	}
}
