//go:build integration
// +build integration

package worker

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest"

	"preprocess-service/internal/config"
	"preprocess-service/internal/domain"
	"preprocess-service/internal/storage/memory"
	"preprocess-service/internal/storage/postgres"
	"preprocess-service/internal/transport"
	"preprocess-service/internal/usecase/ports"
	"preprocess-service/internal/usecase/preprocess"
	"preprocess-service/internal/usecase/submit"
	preprocessv1 "preprocess-service/proto/backend/preprocess/v1"
)

func TestSubmitAndWorkerHappyPath(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	t.Setenv("CONFIG_PATH", filepath.Join("config", "testdata", "config.yaml"))
	cfg := config.LoadTestConfig(t)

	db, terminate := setupPostgres(ctx, t)
	defer terminate()

	repo := postgres.NewPreprocessRepository(db, zaptest.NewLogger(t))
	service, err := submit.NewService(repo, cfg.Preprocess.TargetBucket, cfg.Preprocess.TargetKeyPrefix, zaptest.NewLogger(t))
	if err != nil {
		t.Fatalf("init submit service: %v", err)
	}
	handler := transport.NewPreprocessHandler(service, zaptest.NewLogger(t))

	targetKey := path.Join(cfg.Preprocess.TargetKeyPrefix, "file.docx")
	submitResp, err := handler.SubmitPreprocess(ctx, &preprocessv1.SubmitPreprocessRequest{
		SourceBucket:  cfg.Preprocess.SourceBucket,
		SourceKey:     path.Join("incoming", "file.docx"),
		SourceEtag:    "etag-1",
		TargetBucket:  cfg.Preprocess.TargetBucket,
		TargetKey:     targetKey,
		DocumentId:    "doc-1",
		CorrelationId: "corr-1",
	})
	if err != nil {
		t.Fatalf("submit failed: %v", err)
	}

	queueJob, found, err := repo.NextForProcessing(ctx)
	if err != nil {
		t.Fatalf("pickJob failed: %v", err)
	}
	if !found {
		t.Fatal("expected job to be available")
	}

	if queueJob.ID != submitResp.GetJobId() {
		t.Fatalf("unexpected job id: %s", queueJob.ID)
	}

	if queueJob.TargetBucket == "" {
		queueJob.TargetBucket = cfg.Preprocess.TargetBucket
	}
	if queueJob.TargetKey == "" {
		queueJob.TargetKey = path.Join(cfg.Preprocess.TargetKeyPrefix, "result.docx")
	}

	queueJob.TargetMediaType = preprocess.DefaultTargetMediaType
	if err := repo.MarkDone(ctx, queueJob); err != nil {
		t.Fatalf("markDone failed: %v", err)
	}

	statusResp, err := handler.GetPreprocessStatus(ctx, &preprocessv1.GetPreprocessStatusRequest{JobId: submitResp.GetJobId()})
	if err != nil {
		t.Fatalf("get status failed: %v", err)
	}

	if statusResp.GetStatus() != preprocessv1.PreprocessStatus_PREPROCESS_STATUS_COMPLETED {
		t.Fatalf("unexpected status: %s", statusResp.GetStatus())
	}

	if statusResp.GetDocumentId() != "doc-1" {
		t.Fatalf("unexpected document id: %s", statusResp.GetDocumentId())
	}

	if statusResp.GetErrorCode() != "" || statusResp.GetErrorMessage() != "" {
		t.Fatalf("unexpected error info: code=%s message=%s", statusResp.GetErrorCode(), statusResp.GetErrorMessage())
	}

	if len(statusResp.GetErrorDetails()) != 0 {
		t.Fatalf("expected empty error details, got %#v", statusResp.GetErrorDetails())
	}
}

func TestWorkerRecoversFromProcessorPanic(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	repo := memory.NewRepository()
	now := time.Now().UTC()
	job := domain.PreprocessJob{
		ID:           "job-panic",
		DocumentID:   "doc-panic",
		SourceBucket: "source",
		SourceKey:    "key.docx",
		SourceETag:   "etag-1",
		TargetBucket: "target",
		TargetKey:    "result.docx",
		Status:       domain.StatusPending,
		ErrorDetails: map[string]string{},
		Attempts:     0,
		MaxAttempts:  domain.DefaultMaxAttempts,
		NextRunAt:    now,
		CreatedAt:    now,
		UpdatedAt:    now,
	}

	if err := repo.Create(ctx, job); err != nil {
		t.Fatalf("create job: %v", err)
	}

	logger := zap.NewNop()
	workerCfg := Config{
		WorkingDir:      t.TempDir(),
		OutputDir:       t.TempDir(),
		TargetBucket:    "target",
		TargetMediaType: preprocess.DefaultTargetMediaType,
		Concurrency:     1,
		ProcessTimeout:  time.Second,
		ConvertTimeout:  time.Second,
		Backoff:         10 * time.Millisecond,
		PollInterval:    10 * time.Millisecond,
	}

	wrk, err := New(Dependencies{
		Repo:             repo,
		Downloader:       noopStorage{},
		Uploader:         noopStorage{},
		Converter:        panicConverter{},
		WorkspaceManager: stubWorkspaceManager{},
		Callback:         noopCallback{},
	}, workerCfg, logger)
	if err != nil {
		t.Fatalf("init worker: %v", err)
	}

	jobForProcessing, found, err := repo.NextForProcessing(ctx)
	if err != nil {
		t.Fatalf("next for processing: %v", err)
	}
	if !found {
		t.Fatalf("job not found for processing")
	}

	wrk.processJob(ctx, jobForProcessing)

	stored, found, err := repo.Get(ctx, job.ID)
	if err != nil {
		t.Fatalf("get job: %v", err)
	}
	if !found {
		t.Fatalf("job not found after panic handling")
	}

	if stored.Status == domain.StatusProcessing {
		t.Fatalf("expected job to leave processing state after panic")
	}
	if stored.Status != domain.StatusFailed && stored.Status != domain.StatusStopped {
		t.Fatalf("expected job to be failed or stopped, got %s", stored.Status)
	}
	if stored.ErrorCode != domain.ErrorCodeProcessingError.String() {
		t.Fatalf("unexpected error code: %s", stored.ErrorCode)
	}
	if stored.ErrorMessage == "" {
		t.Fatalf("expected error message to be set")
	}
}

func TestWorkerProcessesJobsSequentially(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	repo := memory.NewRepository()
	now := time.Now().UTC()
	jobOne := newTestJob("job-1", now)
	jobTwo := newTestJob("job-2", now.Add(20*time.Millisecond))

	for _, job := range []domain.PreprocessJob{jobOne, jobTwo} {
		if err := repo.Create(ctx, job); err != nil {
			t.Fatalf("create job %s: %v", job.ID, err)
		}
	}

	converter := newRecordingConverter(map[string]time.Duration{
		jobOne.ID: 200 * time.Millisecond,
		jobTwo.ID: 30 * time.Millisecond,
	})
	workspaceManager := newRecordingWorkspaceManager(t.TempDir())

	workerCfg := Config{
		WorkingDir:      t.TempDir(),
		OutputDir:       t.TempDir(),
		TargetBucket:    "target",
		TargetMediaType: preprocess.DefaultTargetMediaType,
		Concurrency:     1,
		ProcessTimeout:  time.Second,
		ConvertTimeout:  time.Second,
		Backoff:         10 * time.Millisecond,
		PollInterval:    10 * time.Millisecond,
	}

	wrk, err := New(Dependencies{
		Repo:             repo,
		Downloader:       noopStorage{},
		Uploader:         noopStorage{},
		Converter:        converter,
		WorkspaceManager: workspaceManager,
		Callback:         noopCallback{},
	}, workerCfg, zaptest.NewLogger(t))
	if err != nil {
		t.Fatalf("init worker: %v", err)
	}

	runCtx, stop := context.WithCancel(ctx)
	errCh := make(chan error, 1)
	go func() {
		errCh <- wrk.Run(runCtx)
	}()

	waitForJobStatus(ctx, t, repo, jobOne.ID, domain.StatusCompleted)
	waitForJobStatus(ctx, t, repo, jobTwo.ID, domain.StatusCompleted)

	stop()
	if err := <-errCh; err != nil && !errors.Is(err, context.Canceled) {
		t.Fatalf("worker returned error: %v", err)
	}

	firstResult := mustGetJob(t, repo, jobOne.ID)
	secondResult := mustGetJob(t, repo, jobTwo.ID)

	if secondResult.StartedAt.IsZero() || secondResult.FinishedAt.IsZero() {
		t.Fatalf("expected second job to have timestamps, got started_at=%s finished_at=%s", secondResult.StartedAt, secondResult.FinishedAt)
	}
	if !secondResult.StartedAt.After(firstResult.FinishedAt) {
		t.Fatalf("expected second job to start after first finished, got start=%s finish=%s", secondResult.StartedAt, firstResult.FinishedAt)
	}
}

func TestWorkerWaitsForGlobalLockAcrossInstances(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 7*time.Second)
	defer cancel()

	repo := memory.NewRepository()
	now := time.Now().UTC()
	jobOne := newTestJob("job-lock-1", now)
	jobTwo := newTestJob("job-lock-2", now.Add(500*time.Millisecond))

	for _, job := range []domain.PreprocessJob{jobOne, jobTwo} {
		if err := repo.Create(ctx, job); err != nil {
			t.Fatalf("create job %s: %v", job.ID, err)
		}
	}

	converter := newRecordingConverter(map[string]time.Duration{
		jobOne.ID: 150 * time.Millisecond,
		jobTwo.ID: 60 * time.Millisecond,
	})
	workspaceManager := newRecordingWorkspaceManager(t.TempDir())

	workerCfg := Config{
		WorkingDir:      t.TempDir(),
		OutputDir:       t.TempDir(),
		TargetBucket:    "target",
		TargetMediaType: preprocess.DefaultTargetMediaType,
		Concurrency:     1,
		ProcessTimeout:  time.Second,
		ConvertTimeout:  time.Second,
		Backoff:         10 * time.Millisecond,
		PollInterval:    20 * time.Millisecond,
	}

	wrkOne, err := New(Dependencies{
		Repo:             repo,
		Downloader:       noopStorage{},
		Uploader:         noopStorage{},
		Converter:        converter,
		WorkspaceManager: workspaceManager,
		Callback:         noopCallback{},
	}, workerCfg, zaptest.NewLogger(t))
	if err != nil {
		t.Fatalf("init worker 1: %v", err)
	}

	wrkTwo, err := New(Dependencies{
		Repo:             repo,
		Downloader:       noopStorage{},
		Uploader:         noopStorage{},
		Converter:        converter,
		WorkspaceManager: workspaceManager,
		Callback:         noopCallback{},
	}, workerCfg, zaptest.NewLogger(t))
	if err != nil {
		t.Fatalf("init worker 2: %v", err)
	}

	workerOneCtx, workerOneCancel := context.WithCancel(ctx)
	workerTwoCtx, workerTwoCancel := context.WithCancel(ctx)
	defer workerTwoCancel()

	errChOne := make(chan error, 1)
	errChTwo := make(chan error, 1)

	go func() {
		errChOne <- wrkOne.Run(workerOneCtx)
	}()
	go func() {
		errChTwo <- wrkTwo.Run(workerTwoCtx)
	}()

	firstJob := waitForJobStatus(ctx, t, repo, jobOne.ID, domain.StatusCompleted)
	workerOneCancel()

	if err := <-errChOne; err != nil && !errors.Is(err, context.Canceled) {
		t.Fatalf("first worker returned error: %v", err)
	}

	secondJob := waitForJobStatus(ctx, t, repo, jobTwo.ID, domain.StatusCompleted)
	workerTwoCancel()
	if err := <-errChTwo; err != nil && !errors.Is(err, context.Canceled) {
		t.Fatalf("second worker returned error: %v", err)
	}

	if !secondJob.StartedAt.After(firstJob.FinishedAt) {
		t.Fatalf("expected second worker to wait for lock, got second start=%s first finish=%s", secondJob.StartedAt, firstJob.FinishedAt)
	}
}

func newTestJob(id string, nextRunAt time.Time) domain.PreprocessJob {
	now := nextRunAt
	return domain.PreprocessJob{
		ID:              id,
		DocumentID:      fmt.Sprintf("%s-document", id),
		SourceBucket:    "source",
		SourceKey:       fmt.Sprintf("%s.docx", id),
		SourceETag:      fmt.Sprintf("etag-%s", id),
		TargetBucket:    "target",
		TargetKey:       fmt.Sprintf("result-%s.docx", id),
		TargetMediaType: preprocess.DefaultTargetMediaType,
		Status:          domain.StatusPending,
		Attempts:        0,
		MaxAttempts:     domain.DefaultMaxAttempts,
		NextRunAt:       nextRunAt,
		ErrorDetails:    map[string]string{},
		CreatedAt:       now,
		UpdatedAt:       now,
	}
}

func waitForJobStatus(ctx context.Context, t *testing.T, repo domain.PreprocessJobRepository, jobID string, expected domain.PreprocessStatus) domain.PreprocessJob {
	t.Helper()

	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			t.Fatalf("context canceled while waiting for job %s status %s: %v", jobID, expected, ctx.Err())
		case <-ticker.C:
			job, found, err := repo.Get(context.Background(), jobID)
			if err != nil {
				t.Fatalf("get job %s: %v", jobID, err)
			}
			if !found {
				t.Fatalf("job %s not found", jobID)
			}
			if job.Status == expected {
				return job
			}
		}
	}
}

func mustGetJob(t *testing.T, repo domain.PreprocessJobRepository, jobID string) domain.PreprocessJob {
	t.Helper()

	job, found, err := repo.Get(context.Background(), jobID)
	if err != nil {
		t.Fatalf("get job %s: %v", jobID, err)
	}
	if !found {
		t.Fatalf("job %s not found", jobID)
	}

	return job
}

type recordingWorkspaceManager struct {
	baseDir string
}

func newRecordingWorkspaceManager(baseDir string) recordingWorkspaceManager {
	return recordingWorkspaceManager{baseDir: baseDir}
}

func (m recordingWorkspaceManager) Prepare(jobID string) (ports.Workspace, error) {
	jobDir := filepath.Join(m.baseDir, jobID)
	if err := os.MkdirAll(jobDir, 0o755); err != nil {
		return nil, err
	}
	return recordingWorkspace{
		jobID: jobID,
		dir:   jobDir,
	}, nil
}

type recordingWorkspace struct {
	jobID string
	dir   string
}

func (w recordingWorkspace) SourcePath(sourceKey string) (string, error) {
	return filepath.Join(w.dir, fmt.Sprintf("%s-%s", w.jobID, filepath.Base(sourceKey))), nil
}

func (w recordingWorkspace) OutputPath(sourceKey string) (string, error) {
	return filepath.Join(w.dir, fmt.Sprintf("out-%s-%s", w.jobID, filepath.Base(sourceKey))), nil
}

func (w recordingWorkspace) OutputDir() string {
	return w.dir
}

func (w recordingWorkspace) EnsureExists(path string) error {
	if _, err := os.Stat(path); err != nil {
		return err
	}
	return nil
}

func (w recordingWorkspace) Cleanup() error {
	return os.RemoveAll(w.dir)
}

type recordingConverter struct {
	durations map[string]time.Duration
}

func newRecordingConverter(durations map[string]time.Duration) recordingConverter {
	return recordingConverter{durations: durations}
}

func (c recordingConverter) Convert(ctx context.Context, _ string, outDir string) error {
	jobID := filepath.Base(outDir)
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return err
	}

	delay := c.durations[jobID]
	timer := time.NewTimer(delay)
	select {
	case <-ctx.Done():
		timer.Stop()
		return ctx.Err()
	case <-timer.C:
	}

	outputPath := filepath.Join(outDir, "converted.docx")
	if err := os.WriteFile(outputPath, []byte("ok"), 0o644); err != nil {
		return err
	}

	return nil
}

func setupPostgres(ctx context.Context, t *testing.T) (*pgxpool.Pool, func()) {
	t.Helper()

	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker is required for integration test")
	}

	container, err := tcpostgres.RunContainer(ctx,
		testcontainers.WithImage("postgres:16-alpine"),
		tcpostgres.WithDatabase("preprocess"),
		tcpostgres.WithUsername("postgres"),
		tcpostgres.WithPassword("postgres"),
	)
	if err != nil {
		t.Fatalf("start postgres container: %v", err)
	}

	connStr, err := container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		container.Terminate(ctx)
		t.Fatalf("get connection string: %v", err)
	}

	pool, err := pgxpool.New(ctx, connStr)
	if err != nil {
		container.Terminate(ctx)
		t.Fatalf("connect postgres: %v", err)
	}

	if _, err := pool.Exec(ctx, `CREATE EXTENSION IF NOT EXISTS pgcrypto;`); err != nil {
		pool.Close()
		container.Terminate(ctx)
		t.Fatalf("enable pgcrypto: %v", err)
	}

	migrations := []string{
		"migrations/001_create_preprocess_queue.sql",
		"migrations/002_add_worker_columns.sql",
		"migrations/003_add_document_id.sql",
	}

	for _, path := range migrations {
		content, err := os.ReadFile(filepath.Clean(path))
		if err != nil {
			pool.Close()
			container.Terminate(ctx)
			t.Fatalf("read migration %s: %v", path, err)
		}

		if _, err := pool.Exec(ctx, string(content)); err != nil {
			pool.Close()
			container.Terminate(ctx)
			t.Fatalf("apply migration %s: %v", path, err)
		}
	}

	cleanup := func() {
		pool.Close()
		container.Terminate(context.Background())
	}

	return pool, cleanup
}

type noopStorage struct{}

func (noopStorage) Download(context.Context, string, string, string, string) error { return nil }
func (noopStorage) Upload(context.Context, string, string, string, string) error   { return nil }

type panicConverter struct{}

func (panicConverter) Convert(context.Context, string, string) error {
	panic("processor panic")
}

type stubWorkspaceManager struct{}

func (stubWorkspaceManager) Prepare(string) (ports.Workspace, error) { return stubWorkspace{}, nil }

type stubWorkspace struct{}

func (stubWorkspace) SourcePath(string) (string, error) { return "source", nil }
func (stubWorkspace) OutputPath(string) (string, error) { return "output", nil }
func (stubWorkspace) OutputDir() string                 { return "output" }
func (stubWorkspace) EnsureExists(string) error         { return nil }
func (stubWorkspace) Cleanup() error                    { return nil }

type noopCallback struct{}

func (noopCallback) Send(context.Context, string, ports.CallbackPayload, int) (bool, error) {
	return false, nil
}
