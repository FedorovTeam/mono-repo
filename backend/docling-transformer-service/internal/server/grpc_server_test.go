package server

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/mono-repo/docling-transformer-service/internal/config"
	"github.com/mono-repo/docling-transformer-service/internal/converter"
	"github.com/mono-repo/docling-transformer-service/internal/repository"
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

type fakeQueueRepo struct {
	enqueueCalls []repository.EnqueueParams
	pending      int
	processing   int
	waiting      int
	enqueueErr   error
	countErr     error
	queueID      uuid.UUID
}

func (f *fakeQueueRepo) Enqueue(_ context.Context, params repository.EnqueueParams) (uuid.UUID, error) {
	f.enqueueCalls = append(f.enqueueCalls, params)
	if f.enqueueErr != nil {
		return uuid.Nil, f.enqueueErr
	}
	if f.queueID == uuid.Nil {
		f.queueID = uuid.New()
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

func (*fakeQueueRepo) AcquireNext(context.Context) (*repository.TransformationTask, error) {
	return nil, nil
}
func (*fakeQueueRepo) AcquireBatch(context.Context, int) ([]*repository.TransformationTask, error) {
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

type fakeConverter struct {
	submitErr     error
	checkErr      error
	statusResults []converter.StatusCheckResult
	lastRequest   converter.DoclingConvertRequest
}

func (f *fakeConverter) Submit(_ context.Context, req converter.DoclingConvertRequest) (string, error) {
	f.lastRequest = req
	if f.submitErr != nil {
		return "", f.submitErr
	}
	return "job-1", nil
}

func (f *fakeConverter) CheckStatus(context.Context, string) (converter.StatusCheckResult, error) {
	if f.checkErr != nil {
		return converter.StatusCheckResult{}, f.checkErr
	}
	if len(f.statusResults) == 0 {
		return converter.StatusCheckResult{}, nil
	}
	result := f.statusResults[0]
	if len(f.statusResults) > 1 {
		f.statusResults = f.statusResults[1:]
	}
	return result, nil
}

func TestConvertDocumentToMarkdownQueuesTask(t *testing.T) {
	repo := &fakeQueueRepo{queueID: uuid.New()}
	logger := zap.NewNop()

	lis := bufconn.Listen(bufSize)
	srv := newGRPCServer(logger, config.ObservabilityConfig{LoggingEnabled: true})
	transformerv1.RegisterTransformerServiceServer(srv, NewTransformerAPI(repo, "markdown-bucket", 0, nil, 0, 0, 0, logger))

	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)

	ctx := metadata.NewOutgoingContext(context.Background(), metadata.Pairs("x-correlation-id", "corr-123"))
	conn, err := grpc.DialContext(ctx, "bufnet", grpc.WithContextDialer(func(_ context.Context, s string) (net.Conn, error) {
		return lis.Dial()
	}), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("failed to dial bufnet: %v", err)
	}
	t.Cleanup(func() { conn.Close() })

	client := transformerv1.NewTransformerServiceClient(conn)
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

	if resp.GetStatus() != string(repository.StatusPending) {
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

func TestConvertDocumentToMarkdownValidation(t *testing.T) {
	repo := &fakeQueueRepo{}
	logger := zap.NewNop()

	lis := bufconn.Listen(bufSize)
	srv := newGRPCServer(logger, config.ObservabilityConfig{})
	transformerv1.RegisterTransformerServiceServer(srv, NewTransformerAPI(repo, "md", 2, nil, 0, 0, 0, logger))

	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)

	conn, err := grpc.DialContext(context.Background(), "bufnet", grpc.WithContextDialer(func(_ context.Context, s string) (net.Conn, error) {
		return lis.Dial()
	}), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("failed to dial bufnet: %v", err)
	}
	t.Cleanup(func() { conn.Close() })

	client := transformerv1.NewTransformerServiceClient(conn)
	_, err = client.ConvertDocumentToMarkdown(context.Background(), &transformerv1.ConvertDocumentToMarkdownRequest{})

	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("expected invalid argument, got %v", err)
	}

	repo.pending = 1
	repo.processing = 1
	_, err = client.ConvertDocumentToMarkdown(context.Background(), &transformerv1.ConvertDocumentToMarkdownRequest{
		DocumentId:        "doc-123",
		OriginalBucket:    "bucket",
		OriginalObjectKey: "file.docx",
	})

	if status.Code(err) != codes.ResourceExhausted {
		t.Fatalf("expected resource exhausted, got %v", err)
	}
}

func TestConvertDocumentToMarkdownReturnsPendingWithQueueIDOnParallelRequests(t *testing.T) {
	repo := &fakeQueueRepo{}
	logger := zap.NewNop()

	lis := bufconn.Listen(bufSize)
	srv := newGRPCServer(logger, config.ObservabilityConfig{})
	transformerv1.RegisterTransformerServiceServer(srv, NewTransformerAPI(repo, "md", 0, nil, 0, 0, 0, logger))

	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)

	ctx := context.Background()
	conn, err := grpc.DialContext(ctx, "bufnet", grpc.WithContextDialer(func(_ context.Context, _ string) (net.Conn, error) {
		return lis.Dial()
	}), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("failed to dial bufnet: %v", err)
	}
	t.Cleanup(func() { conn.Close() })

	client := transformerv1.NewTransformerServiceClient(conn)

	req := &transformerv1.ConvertDocumentToMarkdownRequest{
		DocumentId:        "doc-123",
		OriginalBucket:    "bucket",
		OriginalObjectKey: "file.docx",
	}

	respCh := make(chan *transformerv1.ConvertDocumentToMarkdownResponse, 2)
	for i := 0; i < 2; i++ {
		go func() {
			resp, callErr := client.ConvertDocumentToMarkdown(ctx, req)
			if callErr != nil {
				t.Errorf("unexpected error: %v", callErr)
				return
			}
			respCh <- resp
		}()
	}

	var responses []*transformerv1.ConvertDocumentToMarkdownResponse
	timeout := time.After(time.Second)
	for len(responses) < 2 {
		select {
		case <-timeout:
			t.Fatalf("timed out waiting for responses")
		case resp := <-respCh:
			responses = append(responses, resp)
		}
	}

	for _, resp := range responses {
		if resp.GetStatus() != string(repository.StatusPending) {
			t.Fatalf("expected pending status, got %s", resp.GetStatus())
		}
		if resp.GetQueueId() == "" {
			t.Fatalf("queue id must be returned")
		}
	}
}

func TestConvertDocxToMarkdownFromURL(t *testing.T) {
	docxContent := []byte("docx-bytes")
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(docxContent)
	}))
	t.Cleanup(ts.Close)

	conv := &fakeConverter{
		statusResults: []converter.StatusCheckResult{
			{},
			{Result: &converter.DoclingConvertResult{Content: []byte("# title"), MediaType: "text/markdown", Charset: "utf-8"}},
		},
	}

	lis := bufconn.Listen(bufSize)
	srv := newGRPCServer(zap.NewNop(), config.ObservabilityConfig{})
	transformerv1.RegisterTransformerServiceServer(srv, NewTransformerAPI(&fakeQueueRepo{}, "md", 0, conv, time.Millisecond, time.Millisecond, 5, zap.NewNop()))
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)

	conn, err := grpc.DialContext(context.Background(), "bufnet", grpc.WithContextDialer(func(_ context.Context, _ string) (net.Conn, error) {
		return lis.Dial()
	}), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial failed: %v", err)
	}
	t.Cleanup(func() { conn.Close() })

	client := transformerv1.NewTransformerServiceClient(conn)
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

func TestConvertDocxToMarkdownValidation(t *testing.T) {
	lis := bufconn.Listen(bufSize)
	srv := newGRPCServer(zap.NewNop(), config.ObservabilityConfig{})
	transformerv1.RegisterTransformerServiceServer(srv, NewTransformerAPI(&fakeQueueRepo{}, "md", 0, &fakeConverter{}, time.Millisecond, time.Millisecond, 1, zap.NewNop()))
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)

	conn, err := grpc.DialContext(context.Background(), "bufnet", grpc.WithContextDialer(func(_ context.Context, _ string) (net.Conn, error) {
		return lis.Dial()
	}), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial failed: %v", err)
	}
	t.Cleanup(func() { conn.Close() })

	client := transformerv1.NewTransformerServiceClient(conn)
	_, err = client.ConvertDocxToMarkdown(context.Background(), &transformerv1.ConvertDocxToMarkdownRequest{})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("expected invalid argument, got %v", err)
	}
}
