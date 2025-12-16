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
    handler := NewPreprocessHandler(repo)

    req := &preprocessv1.SubmitPreprocessRequest{
        SourceBucket: "source",
        SourceKey:    "docx/file.docx",
        SourceEtag:   "etag-1",
        TargetBucket: "target",
        TargetKey:    "out/file.pdf",
        DocumentId:   "doc-1",
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
    handler := NewPreprocessHandler(repo)

    submitResp, err := handler.SubmitPreprocess(context.Background(), &preprocessv1.SubmitPreprocessRequest{
        SourceBucket: "source",
        SourceKey:    "docx/file.docx",
        SourceEtag:   "etag-2",
        TargetBucket: "target",
        TargetKey:    "out/file.pdf",
        DocumentId:   "doc-2",
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

    if statusResp.GetUpdatedAt().AsTime().After(time.Now().Add(1 * time.Minute)) {
        t.Fatalf("updated_at is in the future: %v", statusResp.GetUpdatedAt())
    }
}
