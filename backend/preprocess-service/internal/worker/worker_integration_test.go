//go:build integration
// +build integration

package worker

import (
	"context"
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

	repo := postgres.NewPreprocessRepository(db)
	service, err := submit.NewService(repo, cfg.Preprocess.TargetBucket, cfg.Preprocess.TargetKeyPrefix)
	if err != nil {
		t.Fatalf("init submit service: %v", err)
	}
	handler := transport.NewPreprocessHandler(service)

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

	logger := zap.NewNop().Sugar()
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
