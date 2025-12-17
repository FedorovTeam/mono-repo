package transport

import (
	"context"
	"testing"
	"time"

	"preprocess-service/internal/storage/memory"
	preprocessv1 "preprocess-service/proto/backend/preprocess/v1"
)

func TestSubmitPreprocessIdempotent(t *testing.T) {
	repo := memory.NewJobRepository()
	handler := NewPreprocessHandler(repo, "target", "converted")

	req := &preprocessv1.SubmitPreprocessRequest{
		SourceBucket:  "source",
		SourceKey:     "docx/file.docx",
		SourceEtag:    "etag-1",
		TargetBucket:  "target",
		TargetKey:     "out/file.pdf",
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
	repo := memory.NewJobRepository()
	handler := NewPreprocessHandler(repo, "target", "converted")

	submitResp, err := handler.SubmitPreprocess(context.Background(), &preprocessv1.SubmitPreprocessRequest{
		SourceBucket:  "source",
		SourceKey:     "docx/file.docx",
		SourceEtag:    "etag-2",
		TargetBucket:  "target",
		TargetKey:     "out/file.pdf",
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

	if statusResp.GetTargetBucket() != "target" || statusResp.GetTargetKey() != "out/file.pdf" {
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
	repo := memory.NewJobRepository()
	handler := NewPreprocessHandler(repo, "default-target", "converted")

	req := &preprocessv1.SubmitPreprocessRequest{
		SourceBucket:  "source",
		SourceKey:     "docx/file.docx",
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

	if statusResp.GetTargetBucket() != "default-target" {
		t.Fatalf("expected default target bucket, got %s", statusResp.GetTargetBucket())
	}

	expectedKey := "converted/doc-3.docx"
	if statusResp.GetTargetKey() != expectedKey {
		t.Fatalf("expected default target key %s, got %s", expectedKey, statusResp.GetTargetKey())
	}
}
