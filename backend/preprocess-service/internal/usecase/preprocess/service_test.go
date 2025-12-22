package preprocess

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"go.uber.org/zap"

	"preprocess-service/internal/domain"
	callbackclient "preprocess-service/internal/transport/callback"
	"preprocess-service/internal/usecase/ports"
)

type stubRepo struct{}

func (stubRepo) Create(ctx context.Context, job Job) error { return nil }
func (stubRepo) FindBySource(ctx context.Context, bucket, key, etag string) (Job, bool, error) {
	return Job{}, false, nil
}
func (stubRepo) Get(ctx context.Context, id string) (Job, bool, error) { return Job{}, false, nil }
func (stubRepo) RequeueStuck(ctx context.Context, convertTimeout, backoff time.Duration) error {
	return nil
}
func (stubRepo) NextForProcessing(ctx context.Context) (Job, bool, error) { return Job{}, false, nil }
func (stubRepo) MarkDone(ctx context.Context, job Job) error              { return nil }
func (stubRepo) MarkFailed(ctx context.Context, job Job, update FailureUpdate) error {
	return nil
}
func (stubRepo) TryAcquireWorkerLock(ctx context.Context) (domain.LockHandle, bool, error) {
	return stubLock{}, true, nil
}

type capturingRepo struct {
	failures []FailureUpdate
	statuses []Job
}

func (r *capturingRepo) Create(ctx context.Context, job Job) error { return nil }
func (r *capturingRepo) FindBySource(ctx context.Context, bucket, key, etag string) (Job, bool, error) {
	return Job{}, false, nil
}
func (r *capturingRepo) Get(ctx context.Context, id string) (Job, bool, error) {
	return Job{}, false, nil
}
func (r *capturingRepo) RequeueStuck(ctx context.Context, convertTimeout, backoff time.Duration) error {
	return nil
}
func (r *capturingRepo) NextForProcessing(ctx context.Context) (Job, bool, error) {
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
func (r *capturingRepo) TryAcquireWorkerLock(ctx context.Context) (domain.LockHandle, bool, error) {
	return stubLock{}, true, nil
}

type stubLock struct{}

func (stubLock) Release(context.Context) error { return nil }

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
		callback: callbackclient.NewClient(server.Client(), zap.NewNop()),
		logger:   zap.NewNop(),
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
		callback: callbackclient.NewClient(server.Client(), zap.NewNop()),
		logger:   zap.NewNop(),
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

	fakeWorkspace := &fakeWorkspace{
		sourcePath: "/tmp/preprocess/job-42/source.txt",
		outputPath: "/tmp/preprocess/output/job-42/source.docx",
		outputDir:  "/tmp/preprocess/output/job-42",
	}
	workspaceManager := &fakeWorkspaceManager{workspace: fakeWorkspace}

	svc := &Service{
		cfg: Config{
			TargetBucket:    "default-target",
			TargetMediaType: DefaultTargetMediaType,
		},
		logger:    zap.NewNop(),
		workspace: workspaceManager,
	}

	svc.downloadSourceFn = func(ctx context.Context, job Job, ws ports.Workspace) (string, error) {
		path, err := ws.SourcePath(job.SourceKey)
		if err != nil {
			return "", err
		}
		return path, nil
	}

	var convertedWithDir string
	svc.convertFn = func(ctx context.Context, sourcePath string, ws ports.Workspace) (ports.ConvertResult, error) {
		convertedWithDir = ws.OutputDir()
		return ports.ConvertResult{}, nil
	}

	var uploadedPath string
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

	if workspaceManager.preparedJobID != job.ID {
		t.Fatalf("expected workspace prepared for job %s, got %s", job.ID, workspaceManager.preparedJobID)
	}
	if !fakeWorkspace.cleaned {
		t.Fatalf("expected workspace cleanup to be called")
	}
	if convertedWithDir != fakeWorkspace.outputDir {
		t.Fatalf("unexpected output dir passed to converter: %s", convertedWithDir)
	}
	if uploadedPath != fakeWorkspace.outputPath {
		t.Fatalf("unexpected uploaded path: %s", uploadedPath)
	}
}

func TestProcessTemporaryFailureSchedulesRetry(t *testing.T) {
	t.Helper()

	repo := &capturingRepo{}
	logger := zap.NewNop()
	workspace := &fakeWorkspace{
		sourcePath: "/tmp/source",
		outputPath: "/tmp/output/result.docx",
		outputDir:  "/tmp/output",
	}
	svc := &Service{
		repo:   repo,
		cfg:    Config{WorkingDir: t.TempDir(), OutputDir: t.TempDir(), TargetMediaType: DefaultTargetMediaType, Backoff: time.Second},
		logger: logger,
		policy: domain.NewRetryPolicy(time.Second),
		workspace: &fakeWorkspaceManager{
			workspace: workspace,
		},
	}
	svc.downloadSourceFn = func(ctx context.Context, job Job, ws ports.Workspace) (string, error) {
		return "", temporaryError(domain.ErrorCodeDownloadFailed, errors.New("network glitch"))
	}
	svc.convertFn = func(ctx context.Context, sourcePath string, ws ports.Workspace) (ports.ConvertResult, error) {
		return ports.ConvertResult{}, nil
	}
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
	logger := zap.NewNop()
	workspace := &fakeWorkspace{
		sourcePath: "/tmp/source",
		outputPath: "/tmp/output/result.docx",
		outputDir:  "/tmp/output",
	}
	svc := &Service{
		repo:   repo,
		cfg:    Config{WorkingDir: t.TempDir(), OutputDir: t.TempDir(), TargetMediaType: DefaultTargetMediaType, Backoff: time.Second},
		logger: logger,
		policy: domain.NewRetryPolicy(time.Second),
		workspace: &fakeWorkspaceManager{
			workspace: workspace,
		},
	}
	svc.downloadSourceFn = func(ctx context.Context, job Job, ws ports.Workspace) (string, error) {
		return "", permanentError(domain.ErrorCodeSourceMissing, errors.New("not found"))
	}
	svc.convertFn = func(ctx context.Context, sourcePath string, ws ports.Workspace) (ports.ConvertResult, error) {
		return ports.ConvertResult{}, nil
	}
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

func TestConvertFailureAddsConverterDetails(t *testing.T) {
	t.Helper()

	repo := &capturingRepo{}
	logger := zap.NewNop()
	workspace := &fakeWorkspace{
		sourcePath: "/tmp/source",
		outputPath: "/tmp/output/result.docx",
		outputDir:  "/tmp/output",
	}
	converterResult := ports.ConvertResult{
		ExitCode: 3,
		Stdout:   "sample stdout",
		Stderr:   "sample stderr",
		Duration: 1500 * time.Millisecond,
	}
	svc := &Service{
		repo:      repo,
		cfg:       Config{WorkingDir: t.TempDir(), OutputDir: t.TempDir(), TargetMediaType: DefaultTargetMediaType, Backoff: time.Second, ConvertTimeout: 3 * time.Second},
		logger:    logger,
		policy:    domain.NewRetryPolicy(time.Second),
		workspace: &fakeWorkspaceManager{workspace: workspace},
		converter: failingConverter{result: converterResult, err: errors.New("conversion failed")},
	}
	svc.downloadSourceFn = func(ctx context.Context, job Job, ws ports.Workspace) (string, error) {
		return workspace.sourcePath, nil
	}
	svc.convertFn = svc.convertToDocx
	svc.uploadResultFn = func(ctx context.Context, bucket, key, path string) error { return nil }

	job := Job{
		ID:           "job-convert-fail",
		DocumentID:   "doc-3",
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

	details := repo.failures[0].Details
	if details["converter_exit_code"] != "3" {
		t.Fatalf("unexpected converter exit code detail: %s", details["converter_exit_code"])
	}
	if details["converter_stdout"] != converterResult.Stdout {
		t.Fatalf("unexpected converter stdout detail: %s", details["converter_stdout"])
	}
	if details["converter_stderr"] != converterResult.Stderr {
		t.Fatalf("unexpected converter stderr detail: %s", details["converter_stderr"])
	}
	if details["converter_duration_ms"] == "" {
		t.Fatalf("expected converter duration to be recorded, got empty string")
	}
}

type fakeWorkspaceManager struct {
	workspace     ports.Workspace
	preparedJobID string
	prepareErr    error
}

func (m *fakeWorkspaceManager) Prepare(jobID string) (ports.Workspace, error) {
	m.preparedJobID = jobID
	if m.prepareErr != nil {
		return nil, m.prepareErr
	}
	return m.workspace, nil
}

type fakeWorkspace struct {
	sourcePath    string
	sourceErr     error
	lastSourceKey string
	outputPath    string
	outputErr     error
	lastOutputKey string
	outputDir     string
	existsErr     error
	cleaned       bool
}

func (w *fakeWorkspace) SourcePath(sourceKey string) (string, error) {
	w.lastSourceKey = sourceKey
	if w.sourceErr != nil {
		return "", w.sourceErr
	}
	return w.sourcePath, nil
}

func (w *fakeWorkspace) OutputPath(sourceKey string) (string, error) {
	w.lastOutputKey = sourceKey
	if w.outputErr != nil {
		return "", w.outputErr
	}
	return w.outputPath, nil
}

func (w *fakeWorkspace) OutputDir() string {
	return w.outputDir
}

func (w *fakeWorkspace) EnsureExists(path string) error {
	return w.existsErr
}

func (w *fakeWorkspace) Cleanup() error {
	w.cleaned = true
	return nil
}

type failingConverter struct {
	result ports.ConvertResult
	err    error
}

func (c failingConverter) Convert(context.Context, string, string) (ports.ConvertResult, error) {
	return c.result, c.err
}
