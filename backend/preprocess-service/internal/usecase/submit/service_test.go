package submit

import (
	"context"
	"errors"
	"path"
	"path/filepath"
	"testing"
	"time"

	"go.uber.org/zap/zaptest"
	"google.golang.org/grpc/metadata"

	"preprocess-service/internal/config"
	"preprocess-service/internal/domain"
	"preprocess-service/internal/storage/memory"
)

func loadTestConfig(t *testing.T) config.Config {
	t.Helper()

	t.Setenv("CONFIG_PATH", filepath.Join("config", "testdata", "config.yaml"))

	return config.LoadTestConfig(t)
}

func TestSubmitPreprocessIdempotent(t *testing.T) {
	t.Helper()

	cfg := loadTestConfig(t)

	repo := memory.NewRepository()
	svc, err := NewService(repo, cfg.Preprocess.TargetBucket, cfg.Preprocess.TargetKeyPrefix, zaptest.NewLogger(t))
	if err != nil {
		t.Fatalf("failed to create service: %v", err)
	}

	cmd := SubmitCommand{
		DocumentID:    "doc-1",
		SourceBucket:  cfg.Preprocess.SourceBucket,
		SourceKey:     path.Join("incoming", "file.docx"),
		SourceETag:    "etag-1",
		TargetBucket:  cfg.Preprocess.TargetBucket,
		TargetKey:     path.Join(cfg.Preprocess.TargetKeyPrefix, "file.docx"),
		CorrelationID: "corr-1",
	}

	first, err := svc.SubmitPreprocess(context.Background(), cmd)
	if err != nil {
		t.Fatalf("submit failed: %v", err)
	}

	if first.ID == "" {
		t.Fatal("expected id to be set")
	}

	cmd.CorrelationID = "corr-2"
	second, err := svc.SubmitPreprocess(context.Background(), cmd)
	if err != nil {
		t.Fatalf("idempotent submit failed: %v", err)
	}

	if first.ID != second.ID {
		t.Fatalf("expected same job id, got %s and %s", first.ID, second.ID)
	}
}

func TestSubmitPreprocessUsesDefaults(t *testing.T) {
	t.Helper()

	cfg := loadTestConfig(t)

	repo := memory.NewRepository()
	svc, err := NewService(repo, cfg.Preprocess.TargetBucket, cfg.Preprocess.TargetKeyPrefix, zaptest.NewLogger(t))
	if err != nil {
		t.Fatalf("failed to create service: %v", err)
	}

	cmd := SubmitCommand{
		DocumentID:   "doc-2",
		SourceBucket: cfg.Preprocess.SourceBucket,
		SourceKey:    path.Join("incoming", "file.docx"),
		SourceETag:   "etag-2",
	}

	job, err := svc.SubmitPreprocess(context.Background(), cmd)
	if err != nil {
		t.Fatalf("submit failed: %v", err)
	}

	if job.TargetBucket != cfg.Preprocess.TargetBucket {
		t.Fatalf("unexpected target bucket: %s", job.TargetBucket)
	}

	if job.TargetKey != path.Join(cfg.Preprocess.TargetKeyPrefix, "doc-2.docx") {
		t.Fatalf("unexpected target key: %s", job.TargetKey)
	}
}

func TestSubmitPreprocessValidatesInput(t *testing.T) {
	t.Helper()

	cfg := loadTestConfig(t)

	repo := memory.NewRepository()
	svc, err := NewService(repo, cfg.Preprocess.TargetBucket, cfg.Preprocess.TargetKeyPrefix, zaptest.NewLogger(t))
	if err != nil {
		t.Fatalf("failed to create service: %v", err)
	}

	_, err = svc.SubmitPreprocess(context.Background(), SubmitCommand{})
	if err == nil {
		t.Fatal("expected validation error")
	}

	if !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("expected ErrInvalidRequest, got %v", err)
	}
}

func TestGetStatusReturnsJob(t *testing.T) {
	t.Helper()

	cfg := loadTestConfig(t)

	repo := memory.NewRepository()
	svc, err := NewService(repo, cfg.Preprocess.TargetBucket, cfg.Preprocess.TargetKeyPrefix, zaptest.NewLogger(t))
	if err != nil {
		t.Fatalf("failed to create service: %v", err)
	}

	job := domain.PreprocessJob{
		ID:            "job-1",
		DocumentID:    "doc-1",
		SourceBucket:  "source",
		SourceKey:     "file.docx",
		SourceETag:    "etag",
		Status:        domain.StatusPending,
		ErrorDetails:  map[string]string{},
		UpdatedAt:     time.Now(),
		CorrelationID: "corr-1",
	}

	if err := repo.Create(context.Background(), job); err != nil {
		t.Fatalf("save fixture: %v", err)
	}

	result, err := svc.GetStatus(context.Background(), StatusQuery{JobID: "job-1", CorrelationID: "req-corr"})
	if err != nil {
		t.Fatalf("get status failed: %v", err)
	}

	if result.ID != job.ID {
		t.Fatalf("unexpected job id: %s", result.ID)
	}

	if result.CorrelationID != "req-corr" {
		t.Fatalf("expected correlation override, got %s", result.CorrelationID)
	}
}

func TestSubmitPreprocessUsesContextCorrelationID(t *testing.T) {
	t.Helper()

	cfg := loadTestConfig(t)

	repo := memory.NewRepository()
	svc, err := NewService(repo, cfg.Preprocess.TargetBucket, cfg.Preprocess.TargetKeyPrefix, zaptest.NewLogger(t))
	if err != nil {
		t.Fatalf("failed to create service: %v", err)
	}

	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("x-correlation-id", "ctx-corr"))

	job, err := svc.SubmitPreprocess(ctx, SubmitCommand{
		DocumentID:   "doc-ctx",
		SourceBucket: cfg.Preprocess.SourceBucket,
		SourceKey:    path.Join("incoming", "ctx.docx"),
		SourceETag:   "etag-ctx",
	})
	if err != nil {
		t.Fatalf("submit failed: %v", err)
	}

	if job.CorrelationID != "ctx-corr" {
		t.Fatalf("expected correlation id from context, got %s", job.CorrelationID)
	}

	saved, found, err := repo.Get(context.Background(), job.ID)
	if err != nil {
		t.Fatalf("get job failed: %v", err)
	}
	if !found {
		t.Fatal("expected job to be stored")
	}
	if saved.CorrelationID != "ctx-corr" {
		t.Fatalf("expected stored correlation id from context, got %s", saved.CorrelationID)
	}
}

func TestGetStatusNotFound(t *testing.T) {
	t.Helper()

	cfg := loadTestConfig(t)

	repo := memory.NewRepository()
	svc, err := NewService(repo, cfg.Preprocess.TargetBucket, cfg.Preprocess.TargetKeyPrefix, zaptest.NewLogger(t))
	if err != nil {
		t.Fatalf("failed to create service: %v", err)
	}

	_, err = svc.GetStatus(context.Background(), StatusQuery{JobID: "missing"})
	if !errors.Is(err, ErrJobNotFound) {
		t.Fatalf("expected ErrJobNotFound, got %v", err)
	}
}
