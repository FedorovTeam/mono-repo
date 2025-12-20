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

	"preprocess-service/internal/config"
	"preprocess-service/internal/storage/postgres"
	"preprocess-service/internal/transport"
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
