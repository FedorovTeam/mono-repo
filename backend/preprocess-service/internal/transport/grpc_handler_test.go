package transport

import (
	"context"
	"path"
	"path/filepath"
	"testing"
	"time"

	"go.uber.org/zap/zaptest"
	"google.golang.org/grpc/metadata"

	"preprocess-service/internal/config"
	"preprocess-service/internal/storage/memory"
	"preprocess-service/internal/usecase/submit"
	preprocessv1 "preprocess-service/proto/backend/preprocess/v1"
)

func loadTestConfig(t *testing.T) config.Config {
	t.Helper()

	t.Setenv("CONFIG_PATH", filepath.Join("config", "testdata", "config.yaml"))

	return config.LoadTestConfig(t)
}

func TestSubmitPreprocessIdempotent(t *testing.T) {
	cfg := loadTestConfig(t)

	repo := memory.NewRepository()
	service, err := submit.NewService(repo, cfg.Preprocess.TargetBucket, cfg.Preprocess.TargetKeyPrefix, zaptest.NewLogger(t))
	if err != nil {
		t.Fatalf("init service: %v", err)
	}
	handler := NewPreprocessHandler(service, zaptest.NewLogger(t))

	targetKey := path.Join(cfg.Preprocess.TargetKeyPrefix, "file.pdf")
	req := &preprocessv1.SubmitPreprocessRequest{
		SourceBucket:  cfg.Preprocess.SourceBucket,
		SourceKey:     path.Join("docx", "file.docx"),
		SourceEtag:    "etag-1",
		TargetBucket:  cfg.Preprocess.TargetBucket,
		TargetKey:     targetKey,
		DocumentId:    "doc-1",
		CorrelationId: "corr-1",
	}

	first, err := handler.SubmitPreprocess(context.Background(), req)
	if err != nil {
		t.Fatalf("first submit failed: %v", err)
	}

	if first.GetJobId() == "" {
		t.Fatalf("expected job id to be set")
	}

	req.CorrelationId = "corr-2"
	second, err := handler.SubmitPreprocess(context.Background(), req)
	if err != nil {
		t.Fatalf("second submit failed: %v", err)
	}

	if first.GetJobId() != second.GetJobId() {
		t.Fatalf("expected idempotent job id, got %s and %s", first.GetJobId(), second.GetJobId())
	}

	if second.GetStatus() != preprocessv1.PreprocessStatus_PREPROCESS_STATUS_PENDING {
		t.Fatalf("unexpected status: %s", second.GetStatus())
	}
}

func TestGetPreprocessStatus(t *testing.T) {
	cfg := loadTestConfig(t)

	repo := memory.NewRepository()
	service, err := submit.NewService(repo, cfg.Preprocess.TargetBucket, cfg.Preprocess.TargetKeyPrefix, zaptest.NewLogger(t))
	if err != nil {
		t.Fatalf("init service: %v", err)
	}
	handler := NewPreprocessHandler(service, zaptest.NewLogger(t))

	targetKey := path.Join(cfg.Preprocess.TargetKeyPrefix, "file.pdf")
	submitResp, err := handler.SubmitPreprocess(context.Background(), &preprocessv1.SubmitPreprocessRequest{
		SourceBucket:  cfg.Preprocess.SourceBucket,
		SourceKey:     path.Join("docx", "file.docx"),
		SourceEtag:    "etag-2",
		TargetBucket:  cfg.Preprocess.TargetBucket,
		TargetKey:     targetKey,
		DocumentId:    "doc-2",
		CorrelationId: "corr-3",
	})
	if err != nil {
		t.Fatalf("submit failed: %v", err)
	}

	statusResp, err := handler.GetPreprocessStatus(context.Background(), &preprocessv1.GetPreprocessStatusRequest{
		JobId: submitResp.GetJobId(),
	})
	if err != nil {
		t.Fatalf("status failed: %v", err)
	}

	if statusResp.GetJobId() != submitResp.GetJobId() {
		t.Fatalf("unexpected job id: %s", statusResp.GetJobId())
	}

	if statusResp.GetTargetBucket() != cfg.Preprocess.TargetBucket || statusResp.GetTargetKey() != targetKey {
		t.Fatalf("unexpected target: %s/%s", statusResp.GetTargetBucket(), statusResp.GetTargetKey())
	}

	if statusResp.GetErrorCode() != "" {
		t.Fatalf("unexpected error code: %s", statusResp.GetErrorCode())
	}

	if len(statusResp.GetErrorDetails()) != 0 {
		t.Fatalf("expected empty error details, got %#v", statusResp.GetErrorDetails())
	}

	if statusResp.GetUpdatedAt().AsTime().After(time.Now().Add(1 * time.Minute)) {
		t.Fatalf("updated_at is in the future: %v", statusResp.GetUpdatedAt())
	}
}

func TestSubmitPreprocessUsesDefaults(t *testing.T) {
	cfg := loadTestConfig(t)

	repo := memory.NewRepository()
	service, err := submit.NewService(repo, cfg.Preprocess.TargetBucket, cfg.Preprocess.TargetKeyPrefix, zaptest.NewLogger(t))
	if err != nil {
		t.Fatalf("init service: %v", err)
	}
	handler := NewPreprocessHandler(service, zaptest.NewLogger(t))

	req := &preprocessv1.SubmitPreprocessRequest{
		SourceBucket:  cfg.Preprocess.SourceBucket,
		SourceKey:     path.Join("docx", "file.docx"),
		SourceEtag:    "etag-3",
		DocumentId:    "doc-3",
		CorrelationId: "corr-4",
	}

	resp, err := handler.SubmitPreprocess(context.Background(), req)
	if err != nil {
		t.Fatalf("submit failed: %v", err)
	}

	statusResp, err := handler.GetPreprocessStatus(context.Background(), &preprocessv1.GetPreprocessStatusRequest{
		JobId: resp.GetJobId(),
	})
	if err != nil {
		t.Fatalf("get status failed: %v", err)
	}

	if statusResp.GetTargetBucket() != cfg.Preprocess.TargetBucket {
		t.Fatalf("expected default target bucket, got %s", statusResp.GetTargetBucket())
	}

	expectedKey := path.Join(cfg.Preprocess.TargetKeyPrefix, "doc-3.docx")
	if statusResp.GetTargetKey() != expectedKey {
		t.Fatalf("expected default target key %s, got %s", expectedKey, statusResp.GetTargetKey())
	}
}

func TestSubmitPreprocessUsesContextCorrelationID(t *testing.T) {
	cfg := loadTestConfig(t)

	repo := memory.NewRepository()
	service, err := submit.NewService(repo, cfg.Preprocess.TargetBucket, cfg.Preprocess.TargetKeyPrefix, zaptest.NewLogger(t))
	if err != nil {
		t.Fatalf("init service: %v", err)
	}
	handler := NewPreprocessHandler(service, zaptest.NewLogger(t))

	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("x-correlation-id", "ctx-corr"))

	resp, err := handler.SubmitPreprocess(ctx, &preprocessv1.SubmitPreprocessRequest{
		SourceBucket: cfg.Preprocess.SourceBucket,
		SourceKey:    path.Join("docx", "missing-corr.docx"),
		SourceEtag:   "etag-ctx",
		DocumentId:   "doc-ctx",
	})
	if err != nil {
		t.Fatalf("submit failed: %v", err)
	}

	if resp.GetCorrelationId() != "ctx-corr" {
		t.Fatalf("expected correlation from context, got %s", resp.GetCorrelationId())
	}

	job, found, err := repo.Get(context.Background(), resp.GetJobId())
	if err != nil {
		t.Fatalf("get job: %v", err)
	}
	if !found {
		t.Fatal("job not found")
	}
	if job.CorrelationID != "ctx-corr" {
		t.Fatalf("expected job correlation id from context, got %s", job.CorrelationID)
	}
}
