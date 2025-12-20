package preprocess

import (
	"context"
	"encoding/json"
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
	errInfo := callbackErrorInfo{Code: "convert_failed", Message: "conversion failed"}

	svc.notifyCallback(context.Background(), job, StatusStopped, errInfo)

	if attempts != 2 {
		t.Fatalf("expected two attempts, got %d", attempts)
	}

	if len(payloads) != 2 {
		t.Fatalf("expected two payloads, got %d", len(payloads))
	}

	if payloads[1].Error == nil || payloads[1].Error.Code != errInfo.Code || payloads[1].Error.Message != errInfo.Message {
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
