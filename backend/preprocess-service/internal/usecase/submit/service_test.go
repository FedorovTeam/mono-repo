package submit

import (
	"context"
	"errors"
	"testing"
	"time"

	"preprocess-service/internal/domain"
	"preprocess-service/internal/storage/memory"
)

func TestSubmitPreprocessIdempotent(t *testing.T) {
	t.Helper()

	repo := memory.NewRepository()
	svc, err := NewService(repo, "target", "converted")
	if err != nil {
		t.Fatalf("failed to create service: %v", err)
	}

	cmd := SubmitCommand{
		DocumentID:    "doc-1",
		SourceBucket:  "source",
		SourceKey:     "file.docx",
		SourceETag:    "etag-1",
		TargetBucket:  "target",
		TargetKey:     "converted/file.docx",
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

	repo := memory.NewRepository()
	svc, err := NewService(repo, "default-target", "converted")
	if err != nil {
		t.Fatalf("failed to create service: %v", err)
	}

	cmd := SubmitCommand{
		DocumentID:   "doc-2",
		SourceBucket: "source",
		SourceKey:    "file.docx",
		SourceETag:   "etag-2",
	}

	job, err := svc.SubmitPreprocess(context.Background(), cmd)
	if err != nil {
		t.Fatalf("submit failed: %v", err)
	}

	if job.TargetBucket != "default-target" {
		t.Fatalf("unexpected target bucket: %s", job.TargetBucket)
	}

	if job.TargetKey != "converted/doc-2.docx" {
		t.Fatalf("unexpected target key: %s", job.TargetKey)
	}
}

func TestSubmitPreprocessValidatesInput(t *testing.T) {
	t.Helper()

	repo := memory.NewRepository()
	svc, err := NewService(repo, "target", "converted")
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

	repo := memory.NewRepository()
	svc, err := NewService(repo, "target", "converted")
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

func TestGetStatusNotFound(t *testing.T) {
	t.Helper()

	repo := memory.NewRepository()
	svc, err := NewService(repo, "target", "converted")
	if err != nil {
		t.Fatalf("failed to create service: %v", err)
	}

	_, err = svc.GetStatus(context.Background(), StatusQuery{JobID: "missing"})
	if !errors.Is(err, ErrJobNotFound) {
		t.Fatalf("expected ErrJobNotFound, got %v", err)
	}
}
