package preprocess

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"preprocess-service/internal/domain"
	callbackclient "preprocess-service/internal/transport/callback"
	"preprocess-service/internal/usecase/ports"
)

type stubRepo struct{}

func (stubRepo) NextJobForProcessing(ctx context.Context) (Job, bool, error) {
	return Job{}, false, nil
}

func (stubRepo) MarkDone(ctx context.Context, job Job) error {
	return nil
}

func (stubRepo) MarkFailed(ctx context.Context, job Job, update FailureUpdate) error {
	return nil
}

type capturingRepo struct {
	failures []FailureUpdate
	statuses []Job
}

func (r *capturingRepo) NextJobForProcessing(ctx context.Context) (Job, bool, error) {
	return Job{}, false, nil
}

func (r *capturingRepo) MarkDone(ctx context.Context, job Job) error {
	r.statuses = append(r.statuses, job)
	return nil
}

func (r *capturingRepo) MarkFailed(ctx context.Context, job Job, update FailureUpdate) error {
	r.failures = append(r.failures, update)
	r.statuses = append(r.statuses, job)
	return nil
}

func TestNotifyCallbackSuccess(t *testing.T) {
	t.Helper()

	attempts := 0
	var received ports.CallbackPayload

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++

		if r.Method != http.MethodPost {
			t.Fatalf("expected POST, got %s", r.Method)
		}

		if ct := r.Header.Get("Content-Type"); ct != "application/json" {
			t.Fatalf("unexpected content type: %s", ct)
		}

		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("failed to read body: %v", err)
		}
		defer r.Body.Close()

		if err := json.Unmarshal(body, &received); err != nil {
			t.Fatalf("failed to decode payload: %v", err)
		}

		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	svc := &Service{
		cfg:      Config{Backoff: 5 * time.Millisecond},
		callback: callbackclient.NewClient(server.Client()),
		logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
	}

	job := Job{ID: "job-1", TargetBucket: "bucket", TargetKey: "result.docx", CallbackURL: server.URL}

	svc.notifyCallback(context.Background(), job, StatusDone, callbackErrorInfo{})

	if attempts != 1 {
		t.Fatalf("expected single attempt, got %d", attempts)
	}

	if received.JobID != job.ID || received.Status != StatusDone.String() {
		t.Fatalf("unexpected payload job/status: %#v", received)
	}

	if received.TargetBucket != job.TargetBucket || received.TargetKey != job.TargetKey {
		t.Fatalf("unexpected target: %s/%s", received.TargetBucket, received.TargetKey)
	}

	if received.Error != nil {
		t.Fatalf("expected no error payload, got %+v", received.Error)
	}
}

func TestNotifyCallbackRetry(t *testing.T) {
	t.Helper()

	attempts := 0
	payloads := make([]ports.CallbackPayload, 0)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()

		var payload ports.CallbackPayload
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("failed to read body: %v", err)
		}
		if err := json.Unmarshal(body, &payload); err != nil {
			t.Fatalf("failed to decode payload: %v", err)
		}

		payloads = append(payloads, payload)
		attempts++

		if attempts == 1 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}

		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	svc := &Service{
		cfg:      Config{Backoff: 5 * time.Millisecond},
		callback: callbackclient.NewClient(server.Client()),
		logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
	}

	job := Job{ID: "job-2", TargetBucket: "bucket", TargetKey: "out.docx", CallbackURL: server.URL}
	errInfo := callbackErrorInfo{Code: domain.ErrorCodeConvertFailed, Message: "conversion failed"}

	svc.notifyCallback(context.Background(), job, StatusStopped, errInfo)

	if attempts != 2 {
		t.Fatalf("expected two attempts, got %d", attempts)
	}

	if len(payloads) != 2 {
		t.Fatalf("expected two payloads, got %d", len(payloads))
	}

	if payloads[1].Error == nil || payloads[1].Error.Code != errInfo.Code.String() || payloads[1].Error.Message != errInfo.Message {
		t.Fatalf("unexpected error payload: %+v", payloads[1].Error)
	}
}

func TestHandleJobWritesOutputToConfiguredDir(t *testing.T) {
	t.Helper()

	workingDir := t.TempDir()
	outputDir := t.TempDir()

	svc := &Service{
		cfg: Config{
			WorkingDir:      workingDir,
			OutputDir:       outputDir,
			TargetBucket:    "default-target",
			TargetMediaType: DefaultTargetMediaType,
		},
		logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}

	svc.downloadSourceFn = func(ctx context.Context, job Job, dir string) (string, error) {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return "", err
		}
		sourcePath := filepath.Join(dir, "input.txt")
		return sourcePath, os.WriteFile(sourcePath, []byte("test"), 0o644)
	}

	var uploadedPath string
	svc.convertFn = func(ctx context.Context, sourcePath, outDir string) error {
		if err := os.MkdirAll(outDir, 0o755); err != nil {
			return err
		}
		outputPath := svc.outputFilePath(sourcePath, outDir)
		return os.WriteFile(outputPath, []byte("converted"), 0o644)
	}

	svc.uploadResultFn = func(ctx context.Context, bucket, key, path string) error {
		if bucket != "default-target" {
			return fmt.Errorf("unexpected target bucket: %s", bucket)
		}

		uploadedPath = path
		return nil
	}

	job := Job{
		ID:            "job-42",
		DocumentID:    "doc-42",
		SourceBucket:  "source",
		SourceKey:     "input.txt",
		SourceETag:    "etag-42",
		TargetKey:     "output/doc.docx",
		Status:        StatusPending,
		MaxAttempts:   domain.DefaultMaxAttempts,
		NextRunAt:     time.Now().UTC(),
		CorrelationID: "corr-42",
	}

	if err := svc.handleJob(context.Background(), &job); err != nil {
		t.Fatalf("handleJob returned error: %v", err)
	}

	expectedOutput := filepath.Join(outputDir, job.ID, "input.docx")
	if _, err := os.Stat(expectedOutput); err != nil {
		t.Fatalf("expected output file at %s: %v", expectedOutput, err)
	}

	if uploadedPath != expectedOutput {
		t.Fatalf("unexpected uploaded path: %s", uploadedPath)
	}
}

func TestProcessTemporaryFailureSchedulesRetry(t *testing.T) {
	t.Helper()

	repo := &capturingRepo{}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc := &Service{
		repo:   repo,
		cfg:    Config{WorkingDir: t.TempDir(), OutputDir: t.TempDir(), TargetMediaType: DefaultTargetMediaType, Backoff: time.Second},
		logger: logger,
		policy: domain.NewRetryPolicy(time.Second),
	}
	svc.downloadSourceFn = func(ctx context.Context, job Job, dir string) (string, error) {
		return "", temporaryError(domain.ErrorCodeDownloadFailed, errors.New("network glitch"))
	}
	svc.convertFn = func(ctx context.Context, sourcePath, outDir string) error { return nil }
	svc.uploadResultFn = func(ctx context.Context, bucket, key, path string) error { return nil }

	job := Job{
		ID:           "job-temp",
		DocumentID:   "doc-1",
		SourceBucket: "src",
		SourceKey:    "file.doc",
		SourceETag:   "etag",
		TargetBucket: "bucket",
		TargetKey:    "out.docx",
		Status:       StatusProcessing,
		Attempts:     1,
		MaxAttempts:  3,
		NextRunAt:    time.Now().UTC(),
	}

	err := svc.Process(context.Background(), job)
	if err == nil {
		t.Fatalf("expected error from process")
	}

	if len(repo.failures) != 1 {
		t.Fatalf("expected one failure update, got %d", len(repo.failures))
	}

	update := repo.failures[0]
	if update.Status != domain.StatusFailed {
		t.Fatalf("expected status failed, got %s", update.Status)
	}
	if update.Code != domain.ErrorCodeDownloadFailed {
		t.Fatalf("unexpected error code: %s", update.Code)
	}
	if !update.NextRunAt.After(job.NextRunAt) {
		t.Fatalf("expected next run to be scheduled in future, got %s", update.NextRunAt)
	}
}

func TestProcessPermanentFailureStops(t *testing.T) {
	t.Helper()

	repo := &capturingRepo{}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc := &Service{
		repo:   repo,
		cfg:    Config{WorkingDir: t.TempDir(), OutputDir: t.TempDir(), TargetMediaType: DefaultTargetMediaType, Backoff: time.Second},
		logger: logger,
		policy: domain.NewRetryPolicy(time.Second),
	}
	svc.downloadSourceFn = func(ctx context.Context, job Job, dir string) (string, error) {
		return "", permanentError(domain.ErrorCodeSourceMissing, errors.New("not found"))
	}
	svc.convertFn = func(ctx context.Context, sourcePath, outDir string) error { return nil }
	svc.uploadResultFn = func(ctx context.Context, bucket, key, path string) error { return nil }

	job := Job{
		ID:           "job-stop",
		DocumentID:   "doc-2",
		SourceBucket: "src",
		SourceKey:    "file.doc",
		SourceETag:   "etag",
		TargetBucket: "bucket",
		TargetKey:    "out.docx",
		Status:       StatusProcessing,
		Attempts:     2,
		MaxAttempts:  2,
		NextRunAt:    time.Now().UTC(),
	}

	err := svc.Process(context.Background(), job)
	if err == nil {
		t.Fatalf("expected error from process")
	}

	if len(repo.failures) != 1 {
		t.Fatalf("expected one failure update, got %d", len(repo.failures))
	}

	update := repo.failures[0]
	if update.Status != domain.StatusStopped {
		t.Fatalf("expected status stopped, got %s", update.Status)
	}
	if update.Code != domain.ErrorCodeSourceMissing {
		t.Fatalf("unexpected error code: %s", update.Code)
	}
	if update.NextRunAt.IsZero() {
		t.Fatalf("expected next run time to be set, got %s", update.NextRunAt)
	}
}
