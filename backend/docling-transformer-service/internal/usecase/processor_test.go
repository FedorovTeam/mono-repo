package usecase

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/mono-repo/docling-transformer-service/internal/domain"
	transformations "github.com/mono-repo/docling-transformer-service/internal/domain/transformations"
	"github.com/mono-repo/docling-transformer-service/internal/infrastructure/converter"
	repo "github.com/mono-repo/docling-transformer-service/internal/repository"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

type pollingRepoStub struct {
	updateInfoCalls   int
	markDoneCalls     int
	markWaitingCalls  int
	lastNextPoll      time.Time
	lastPollCount     int
	lastJobID         string
	restored          int
	restoreCalls      int
	restoreStuck      int
	failedStuck       int
	restoreStuckCalls int
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
func (p *pollingRepoStub) MarkDoclingWaiting(ctx context.Context, id uuid.UUID, jobID string, pollCount int, nextPoll time.Time) error {
	p.markWaitingCalls++
	p.lastJobID = jobID
	p.lastPollCount = pollCount
	p.lastNextPoll = nextPoll
	return nil
}
func (p *pollingRepoStub) ActivateWaiting(context.Context, time.Time, time.Duration) (int, int, error) {
	return 0, 0, nil
}
func (p *pollingRepoStub) RestoreProcessing(context.Context) (int, error) {
	p.restoreCalls++
	return p.restored, nil
}
func (p *pollingRepoStub) RestoreStuckProcessing(context.Context, time.Duration, int, int) (int, int, error) {
	p.restoreStuckCalls++
	return p.restoreStuck, p.failedStuck, nil
}
func (p *pollingRepoStub) CleanupFinished(context.Context, time.Duration) (int, error) { return 0, nil }
func (p *pollingRepoStub) CountByStatus(context.Context) (int, int, int, error)        { return 0, 0, 0, nil }

type statusOnlyConverter struct {
	submitCalled bool
}

func (c *statusOnlyConverter) Submit(context.Context, domain.ConvertRequest) (string, error) {
	c.submitCalled = true
	return "", nil
}

func (c *statusOnlyConverter) CheckStatus(context.Context, string) (domain.StatusCheckResult, error) {
	return domain.StatusCheckResult{Status: "success", Result: &domain.ConvertResult{Content: []byte("markdown"), MediaType: "text/markdown"}}, nil
}

type staticResultConverter struct {
	result domain.ConvertResult
}

func (c *staticResultConverter) Submit(context.Context, domain.ConvertRequest) (string, error) {
	return "", nil
}

func (c *staticResultConverter) CheckStatus(context.Context, string) (domain.StatusCheckResult, error) {
	return domain.StatusCheckResult{Status: "success", Result: &c.result}, nil
}

type trackingStorage struct {
	downloadCalls int
	uploaded      []byte
	contentType   string
}

func (s *trackingStorage) Download(context.Context, string, string) ([]byte, error) {
	s.downloadCalls++
	return []byte("original"), nil
}

func (s *trackingStorage) Upload(_ context.Context, _, _ string, contentType string, content []byte) error {
	s.uploaded = append([]byte(nil), content...)
	s.contentType = contentType
	return nil
}

type submitOnlyConverter struct {
	jobID        string
	submitCalled bool
	checkCalled  bool
}

func (c *submitOnlyConverter) Submit(_ context.Context, req domain.ConvertRequest) (string, error) {
	c.submitCalled = true
	if c.jobID == "" {
		c.jobID = "job-from-submit"
	}
	if req.ContentType == "" {
		return "", fmt.Errorf("content type is empty")
	}

	return c.jobID, nil
}

func (c *submitOnlyConverter) CheckStatus(context.Context, string) (domain.StatusCheckResult, error) {
	c.checkCalled = true
	return domain.StatusCheckResult{}, errors.New("status should not be called")
}

type recordingStorage struct {
	downloadBucket string
	downloadKey    string
	uploadCalls    int
}

func (s *recordingStorage) Download(_ context.Context, bucket, key string) ([]byte, error) {
	s.downloadBucket = bucket
	s.downloadKey = key
	return []byte("doc"), nil
}

func (s *recordingStorage) Upload(context.Context, string, string, string, []byte) error {
	s.uploadCalls++
	return nil
}

func TestHandleTaskPollsWithoutRedownloading(t *testing.T) {
	repo := &pollingRepoStub{}
	storage := &trackingStorage{}
	conv := &statusOnlyConverter{}
	processor := NewProcessor(repo, conv, storage, nil, Config{})

	task := &transformations.TransformationTask{
		ID:                uuid.New(),
		DoclingJobID:      "job-123",
		DoclingPollCount:  2,
		MarkdownBucket:    "md",
		MarkdownObjectKey: "dest/file.md",
		CreatedAt:         time.Now().Add(-time.Minute),
	}

	if err := processor.handleTask(context.Background(), task); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if storage.downloadCalls != 0 {
		t.Fatalf("expected no downloads during polling, got %d", storage.downloadCalls)
	}

	if repo.markDoneCalls != 1 {
		t.Fatalf("expected mark done to be called once, got %d", repo.markDoneCalls)
	}

	if repo.updateInfoCalls != 1 {
		t.Fatalf("expected docling info update once, got %d", repo.updateInfoCalls)
	}

	if conv.submitCalled {
		t.Fatal("submit should not be called when job id is already set")
	}
}

func TestRunPerformsMaintenanceOnStartup(t *testing.T) {
	repo := &pollingRepoStub{restoreStuck: 2, failedStuck: 1}
	storage := &trackingStorage{}
	conv := &statusOnlyConverter{}
	processor := NewProcessor(repo, conv, storage, nil, Config{PollInterval: time.Millisecond, ProcessingTimeout: time.Minute})

	ctx, cancel := context.WithCancel(context.Background())
	processor.Run(ctx)
	cancel()

	if repo.restoreStuckCalls != 1 {
		t.Fatalf("expected stuck restore to be called once, got %d", repo.restoreStuckCalls)
	}
}

func TestPollDoclingJobLogsStatusError(t *testing.T) {
	const responseBody = `{"task_status":"unknown","message":"docling refused"}`

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/v1/status/poll/"):
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(responseBody))
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/v1/result/"):
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"errors":[{"module_name":"worker"}]}`))
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	repo := &pollingRepoStub{}
	storage := &trackingStorage{}
	conv := converter.NewDoclingConverter(converter.Config{Endpoint: server.URL}, zap.NewNop())

	logCore, logs := observer.New(zap.InfoLevel)
	processor := NewProcessor(repo, conv, storage, zap.New(logCore), Config{DoclingMaxAttempts: 1})

	jobID := "job-log-error"
	task := &transformations.TransformationTask{ID: uuid.New(), DoclingJobID: jobID, CreatedAt: time.Now()}

	err := processor.pollDoclingJob(context.Background(), task, jobID, 0)
	if err == nil {
		t.Fatal("expected polling to return error")
	}

	if !strings.Contains(err.Error(), "response="+responseBody) {
		t.Fatalf("expected docling response in error, got: %v", err)
	}

	entries := logs.FilterMessage("docling status check failed").All()
	if len(entries) == 0 {
		t.Fatalf("expected error log entry, got %d logs", logs.Len())
	}

	errorField := fmt.Sprint(entries[0].ContextMap()["error"])
	if !strings.Contains(errorField, "response="+responseBody) {
		t.Fatalf("expected response body in log, got: %s", errorField)
	}
}

func TestPollDoclingJobRetriesOnTemporaryError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/v1/status/poll/"):
			http.Error(w, "temporary outage", http.StatusBadGateway)
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	repo := &pollingRepoStub{}
	storage := &trackingStorage{}
	conv := converter.NewDoclingConverter(converter.Config{Endpoint: server.URL}, zap.NewNop())

	logCore, logs := observer.New(zap.InfoLevel)
	processor := NewProcessor(repo, conv, storage, zap.New(logCore), Config{DoclingInitialInterval: time.Second})

	task := &transformations.TransformationTask{ID: uuid.New(), DoclingJobID: "job-temp", CreatedAt: time.Now()}

	err := processor.pollDoclingJob(context.Background(), task, task.DoclingJobID, 0)
	if !errors.Is(err, errDoclingWaiting) {
		t.Fatalf("expected waiting error, got: %v", err)
	}

	if repo.markWaitingCalls != 1 {
		t.Fatalf("expected MarkDoclingWaiting to be called once, got %d", repo.markWaitingCalls)
	}

	if task.NextPollAt == nil || task.NextPollAt.Before(time.Now()) {
		t.Fatalf("expected next poll to be scheduled in the future, got %v", task.NextPollAt)
	}

	entries := logs.FilterMessage("docling temporary error, will retry").All()
	if len(entries) == 0 {
		t.Fatalf("expected temporary error log, got %d entries", logs.Len())
	}
}

func TestPollDoclingJobNormalizesMediaType(t *testing.T) {
	cases := []struct {
		name       string
		mediaType  string
		charset    string
		expectedCT string
	}{
		{name: "default markdown", mediaType: "", expectedCT: "text/markdown; charset=utf-8"},
		{name: "plain text", mediaType: "text/plain", expectedCT: "text/plain; charset=utf-8"},
		{name: "charset already set", mediaType: "text/html; charset=utf-8", expectedCT: "text/html; charset=utf-8"},
		{name: "docling charset", mediaType: "text/markdown", charset: "koi8-r", expectedCT: "text/markdown; charset=koi8-r"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repo := &pollingRepoStub{}
			storage := &trackingStorage{}
			conv := &staticResultConverter{result: domain.ConvertResult{Content: []byte("body"), MediaType: tc.mediaType, Charset: tc.charset}}
			processor := NewProcessor(repo, conv, storage, zap.NewNop(), Config{})

			task := &transformations.TransformationTask{ID: uuid.New(), DoclingJobID: "job", MarkdownBucket: "bucket", MarkdownObjectKey: "object", CreatedAt: time.Now()}

			if err := processor.pollDoclingJob(context.Background(), task, task.DoclingJobID, 0); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if storage.contentType != tc.expectedCT {
				t.Fatalf("expected content type %q, got %q", tc.expectedCT, storage.contentType)
			}
		})
	}
}

func TestPollDoclingJobStoresUtf8ForCyrillicMarkdown(t *testing.T) {
	repo := &pollingRepoStub{}
	storage := &trackingStorage{}
	conv := &staticResultConverter{result: domain.ConvertResult{Content: []byte("# Заголовок\nТекст"), MediaType: "text/markdown"}}
	processor := NewProcessor(repo, conv, storage, zap.NewNop(), Config{})

	task := &transformations.TransformationTask{ID: uuid.New(), DoclingJobID: "job", MarkdownBucket: "bucket", MarkdownObjectKey: "object", CreatedAt: time.Now()}

	if err := processor.pollDoclingJob(context.Background(), task, task.DoclingJobID, 0); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if storage.contentType != "text/markdown; charset=utf-8" {
		t.Fatalf("expected utf-8 charset for cyrillic markdown, got %q", storage.contentType)
	}
}

func TestHandleTaskUsesConfiguredMarkdownBucket(t *testing.T) {
	repo := &pollingRepoStub{}
	storage := &recordingStorage{}
	conv := &submitOnlyConverter{jobID: "job-configured-bucket"}
	processor := NewProcessor(repo, conv, storage, zap.NewNop(), Config{MarkdownBucket: "markdown-output"})

	task := &transformations.TransformationTask{
		ID:                uuid.New(),
		OriginalBucket:    "source-bucket",
		OriginalObjectKey: "uploads/file.DOCX",
	}

	err := processor.handleTask(context.Background(), task)
	if !errors.Is(err, errDoclingWaiting) {
		t.Fatalf("expected errDoclingWaiting, got: %v", err)
	}

	if task.MarkdownBucket != "markdown-output" {
		t.Fatalf("expected markdown bucket from config, got %q", task.MarkdownBucket)
	}

	if task.MarkdownObjectKey != "uploads/file.md" {
		t.Fatalf("expected markdown object key to be derived from original, got %q", task.MarkdownObjectKey)
	}

	if repo.markWaitingCalls != 1 {
		t.Fatalf("expected docling job to be stored once, got %d", repo.markWaitingCalls)
	}

	if repo.lastJobID != "job-configured-bucket" {
		t.Fatalf("expected job id to be saved, got %q", repo.lastJobID)
	}

	if storage.downloadBucket != "source-bucket" || storage.downloadKey != "uploads/file.DOCX" {
		t.Fatalf("unexpected download parameters: bucket=%q key=%q", storage.downloadBucket, storage.downloadKey)
	}

	if conv.checkCalled {
		t.Fatal("status check should not be called during submission phase")
	}
}

func TestHandleTaskFailsWithoutMarkdownBucket(t *testing.T) {
	repo := &pollingRepoStub{}
	storage := &recordingStorage{}
	conv := &submitOnlyConverter{}
	processor := NewProcessor(repo, conv, storage, zap.NewNop(), Config{})

	task := &transformations.TransformationTask{
		ID:                uuid.New(),
		OriginalBucket:    "source-bucket",
		OriginalObjectKey: "uploads/file.DOCX",
	}

	err := processor.handleTask(context.Background(), task)
	if err == nil || !strings.Contains(err.Error(), "markdown bucket is empty") {
		t.Fatalf("expected empty bucket error, got: %v", err)
	}

	if repo.markWaitingCalls != 0 {
		t.Fatalf("expected docling job not to be stored, got %d calls", repo.markWaitingCalls)
	}

	if conv.submitCalled {
		t.Fatal("submit should not be called when markdown bucket is missing")
	}

	if storage.downloadBucket != "" || storage.downloadKey != "" {
		t.Fatalf("expected no downloads without bucket, got bucket=%q key=%q", storage.downloadBucket, storage.downloadKey)
	}
}
