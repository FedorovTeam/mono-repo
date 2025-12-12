package grpcapi

import (
        "context"
        "net"
        "net/http"
        "net/http/httptest"
        "testing"
        "time"

        "github.com/google/uuid"
        "github.com/mono-repo/docling-transformer-service/internal/domain"
        "github.com/mono-repo/docling-transformer-service/internal/usecase"
        transformerv1 "github.com/mono-repo/docling-transformer-service/proto/backend/transformer/v1"
        "go.uber.org/zap"
        "google.golang.org/grpc"
        "google.golang.org/grpc/codes"
        "google.golang.org/grpc/credentials/insecure"
        "google.golang.org/grpc/metadata"
        "google.golang.org/grpc/status"
        "google.golang.org/grpc/test/bufconn"
)

const bufSize = 1024 * 1024

func TestConvertDocumentToMarkdownQueuesTask(t *testing.T) {
        repo := &fakeQueueRepo{queueID: uuid.New()}
        conv := &fakeConverter{}

        client, cleanup := startTestClient(t, repo, conv, usecase.TransformationConfig{MarkdownBucket: "markdown-bucket", PollInterval: time.Millisecond, PollMaxInterval: time.Millisecond, PollAttempts: 3})
        t.Cleanup(cleanup)

        ctx := metadata.NewOutgoingContext(context.Background(), metadata.Pairs("x-correlation-id", "corr-123"))
        resp, err := client.ConvertDocumentToMarkdown(ctx, &transformerv1.ConvertDocumentToMarkdownRequest{
                DocumentId:        "doc-123",
                OriginalBucket:    "bucket-orig",
                OriginalObjectKey: "path/file.docx",
                MarkdownObjectKey: "md/file.md",
        })
        if err != nil {
                t.Fatalf("unexpected error: %v", err)
        }

        if resp.GetQueueId() != repo.queueID.String() {
                t.Fatalf("unexpected queue id: %s", resp.GetQueueId())
        }

        if resp.GetStatus() != string(domain.StatusPending) {
                t.Fatalf("unexpected status: %s", resp.GetStatus())
        }

        if len(repo.enqueueCalls) != 1 {
                t.Fatalf("expected enqueue to be called once, got %d", len(repo.enqueueCalls))
        }

        enqueued := repo.enqueueCalls[0]
        if enqueued.MarkdownBucket != "markdown-bucket" {
                t.Fatalf("markdown bucket fallback not applied: %s", enqueued.MarkdownBucket)
        }
        if enqueued.MarkdownObjectKey != "md/file.md" {
                t.Fatalf("markdown key mismatch: %s", enqueued.MarkdownObjectKey)
        }
        if enqueued.CorrelationID != "corr-123" {
                t.Fatalf("correlation id not propagated: %s", enqueued.CorrelationID)
        }
}

func TestConvertDocumentToMarkdownWaitsForResult(t *testing.T) {
        doneID := uuid.New()
        repo := &fakeQueueRepo{
                latestTask: &domain.TransformationTask{ID: doneID, Status: domain.StatusProcessing, MarkdownBucket: "md-bucket"},
                byID: map[uuid.UUID]*domain.TransformationTask{
                        doneID: {ID: doneID, Status: domain.StatusDone, MarkdownBucket: "md-bucket", MarkdownObjectKey: "ready.md"},
                },
        }

        client, cleanup := startTestClient(t, repo, &fakeConverter{}, usecase.TransformationConfig{MarkdownBucket: "fallback-md", PollInterval: time.Millisecond, PollMaxInterval: time.Millisecond, PollAttempts: 2})
        t.Cleanup(cleanup)

        resp, err := client.ConvertDocumentToMarkdown(context.Background(), &transformerv1.ConvertDocumentToMarkdownRequest{
                DocumentId:        "doc-123",
                OriginalBucket:    "bucket-orig",
                OriginalObjectKey: "path/file.docx",
                WaitForResult:     true,
        })
        if err != nil {
                t.Fatalf("unexpected error: %v", err)
        }

        if resp.GetStatus() != string(domain.StatusDone) {
                t.Fatalf("expected done status, got %s", resp.GetStatus())
        }
        if resp.GetMarkdownObjectKey() != "ready.md" {
                t.Fatalf("expected ready key, got %s", resp.GetMarkdownObjectKey())
        }
}

func TestConvertDocumentToMarkdownValidation(t *testing.T) {
        repo := &fakeQueueRepo{}
        client, cleanup := startTestClient(t, repo, &fakeConverter{}, usecase.TransformationConfig{MarkdownBucket: "md", QueueLimit: 1, PollInterval: time.Millisecond, PollMaxInterval: time.Millisecond, PollAttempts: 1})
        t.Cleanup(cleanup)

        _, err := client.ConvertDocumentToMarkdown(context.Background(), &transformerv1.ConvertDocumentToMarkdownRequest{})
        if status.Code(err) != codes.InvalidArgument {
                t.Fatalf("expected invalid argument, got %v", err)
        }

        repo.pending = 1
        _, err = client.ConvertDocumentToMarkdown(context.Background(), &transformerv1.ConvertDocumentToMarkdownRequest{
                DocumentId:        "doc-123",
                OriginalBucket:    "bucket",
                OriginalObjectKey: "file.docx",
        })

        if status.Code(err) != codes.ResourceExhausted {
                t.Fatalf("expected resource exhausted, got %v", err)
        }
}

func TestConvertDocxToMarkdownFromURL(t *testing.T) {
        docxContent := []byte("docx-bytes")
        ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
                _, _ = w.Write(docxContent)
        }))
        t.Cleanup(ts.Close)

        conv := &fakeConverter{
                statusResults: []domain.StatusCheckResult{
                        {},
                        {Result: &domain.ConvertResult{Content: []byte("# title"), MediaType: "text/markdown", Charset: "utf-8"}},
                },
        }

        client, cleanup := startTestClient(t, &fakeQueueRepo{}, conv, usecase.TransformationConfig{MarkdownBucket: "md", PollInterval: time.Millisecond, PollMaxInterval: time.Millisecond, PollAttempts: 3})
        t.Cleanup(cleanup)

        resp, err := client.ConvertDocxToMarkdown(context.Background(), &transformerv1.ConvertDocxToMarkdownRequest{Source: &transformerv1.ConvertDocxToMarkdownRequest_DocxUrl{DocxUrl: ts.URL + "/file.docx"}})
        if err != nil {
                t.Fatalf("call failed: %v", err)
        }

        if resp.GetMarkdown() != "# title" {
                t.Fatalf("unexpected markdown: %s", resp.GetMarkdown())
        }
        if string(conv.lastRequest.Content) != string(docxContent) {
                t.Fatalf("converter did not receive docx content")
        }
        if conv.lastRequest.FileName != "file.docx" {
                t.Fatalf("filename not derived from url: %s", conv.lastRequest.FileName)
        }
}

func startTestClient(t *testing.T, repo *fakeQueueRepo, conv *fakeConverter, cfg usecase.TransformationConfig) (transformerv1.TransformerServiceClient, func()) {
        t.Helper()

        uc := usecase.NewTransformationUseCase(repo, conv, cfg, zap.NewNop(), &http.Client{Timeout: time.Second})
        server := grpc.NewServer()
        transformerv1.RegisterTransformerServiceServer(server, NewTransformerAPI(uc, zap.NewNop()))

        lis := bufconn.Listen(bufSize)
        go func() {
                if err := server.Serve(lis); err != nil {
                        t.Errorf("server failed: %v", err)
                }
        }()

        conn, err := grpc.DialContext(context.Background(), "bufnet", grpc.WithContextDialer(func(_ context.Context, _ string) (net.Conn, error) {
                return lis.Dial()
        }), grpc.WithTransportCredentials(insecure.NewCredentials()))
        if err != nil {
                t.Fatalf("failed to dial bufnet: %v", err)
        }

        client := transformerv1.NewTransformerServiceClient(conn)

        cleanup := func() {
                conn.Close()
                server.Stop()
        }

        return client, cleanup
}

type fakeConverter struct {
        submitErr     error
        checkErr      error
        statusResults []domain.StatusCheckResult
        lastRequest   domain.ConvertRequest
}

func (f *fakeConverter) Submit(_ context.Context, req domain.ConvertRequest) (string, error) {
        f.lastRequest = req
        if f.submitErr != nil {
                return "", f.submitErr
        }
        return "job-1", nil
}

func (f *fakeConverter) CheckStatus(context.Context, string) (domain.StatusCheckResult, error) {
        if f.checkErr != nil {
                return domain.StatusCheckResult{}, f.checkErr
        }
        if len(f.statusResults) == 0 {
                return domain.StatusCheckResult{}, nil
        }
        result := f.statusResults[0]
        if len(f.statusResults) > 1 {
                f.statusResults = f.statusResults[1:]
        }
        return result, nil
}

type fakeQueueRepo struct {
        enqueueCalls []domain.EnqueueParams
        pending      int
        processing   int
        waiting      int
        enqueueErr   error
        countErr     error
        queueID      uuid.UUID
        latestTask   *domain.TransformationTask
        byID         map[uuid.UUID]*domain.TransformationTask
}

func (f *fakeQueueRepo) Enqueue(_ context.Context, params domain.EnqueueParams) (uuid.UUID, error) {
        f.enqueueCalls = append(f.enqueueCalls, params)
        if f.enqueueErr != nil {
                return uuid.Nil, f.enqueueErr
        }
        if f.queueID == uuid.Nil {
                f.queueID = uuid.New()
        }
        if f.byID != nil {
                f.byID[f.queueID] = &domain.TransformationTask{ID: f.queueID, Status: domain.StatusPending, MarkdownBucket: params.MarkdownBucket, MarkdownObjectKey: params.MarkdownObjectKey}
        }
        return f.queueID, nil
}

func (f *fakeQueueRepo) CountActive(context.Context) (int, error) {
        if f.countErr != nil {
                return 0, f.countErr
        }
        return f.pending + f.processing + f.waiting, nil
}

func (f *fakeQueueRepo) CountByStatus(context.Context) (int, int, int, error) {
        if f.countErr != nil {
                return 0, 0, 0, f.countErr
        }

        return f.pending, f.processing, f.waiting, nil
}

func (*fakeQueueRepo) AcquireNext(context.Context) (*domain.TransformationTask, error) { return nil, nil }
func (*fakeQueueRepo) AcquireBatch(context.Context, int) ([]*domain.TransformationTask, error) {
        return nil, nil
}
func (f *fakeQueueRepo) GetLatestByDocument(context.Context, string) (*domain.TransformationTask, error) {
        return f.latestTask, nil
}
func (f *fakeQueueRepo) GetByID(_ context.Context, id uuid.UUID) (*domain.TransformationTask, error) {
        if f.byID == nil {
                return nil, nil
        }
        if task, ok := f.byID[id]; ok {
                return task, nil
        }
        return nil, nil
}
func (*fakeQueueRepo) MarkDone(context.Context, uuid.UUID, string, string) error { return nil }
func (*fakeQueueRepo) MarkFailed(context.Context, uuid.UUID, string) error       { return nil }
func (*fakeQueueRepo) UpdateDoclingInfo(context.Context, uuid.UUID, string, int) error {
        return nil
}
func (*fakeQueueRepo) MarkDoclingWaiting(context.Context, uuid.UUID, string, int, time.Time) error {
        return nil
}
func (*fakeQueueRepo) ActivateWaiting(context.Context, time.Time, time.Duration) (int, int, error) {
        return 0, 0, nil
}
func (*fakeQueueRepo) RestoreProcessing(context.Context) (int, error) { return 0, nil }
func (*fakeQueueRepo) RestoreStuckProcessing(context.Context, time.Duration, int, int) (int, int, error) {
        return 0, 0, nil
}
func (*fakeQueueRepo) CleanupFinished(context.Context, time.Duration) (int, error) { return 0, nil }

