package grpcapi

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/mono-repo/docling-transformer-service/internal/domain"
	transformations "github.com/mono-repo/docling-transformer-service/internal/domain/transformations"
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
	queueID := uuid.New()
	uc := &mockTransformerUseCase{}
	uc.submitFunc = func(_ context.Context, req usecase.SubmitConversionRequest) (usecase.SubmitConversionResult, error) {
		uc.lastSubmit = req
		return usecase.SubmitConversionResult{
			Task: &transformations.TransformationTask{
				ID:                queueID,
				Status:            transformations.StatusPending,
				MarkdownBucket:    "markdown-bucket",
				MarkdownObjectKey: "md/file.md",
			},
			MarkdownBucket:    "markdown-bucket",
			MarkdownObjectKey: "md/file.md",
		}, nil
	}

	client, cleanup := startTestClient(t, uc)
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

	if resp.GetQueueId() != queueID.String() {
		t.Fatalf("unexpected queue id: %s", resp.GetQueueId())
	}

	if resp.GetStatus() != string(transformations.StatusPending) {
		t.Fatalf("unexpected status: %s", resp.GetStatus())
	}

	if resp.GetMarkdownBucket() != "markdown-bucket" || resp.GetMarkdownObjectKey() != "md/file.md" {
		t.Fatalf("unexpected markdown location: %s/%s", resp.GetMarkdownBucket(), resp.GetMarkdownObjectKey())
	}

	if uc.lastSubmit.CorrelationID != "corr-123" {
		t.Fatalf("correlation id not propagated: %s", uc.lastSubmit.CorrelationID)
	}

	if resp.GetCorrelationId() != "corr-123" {
		t.Fatalf("response correlation id mismatch: %s", resp.GetCorrelationId())
	}
}

func TestConvertDocumentToMarkdownRespectsRequestCorrelation(t *testing.T) {
	queueID := uuid.New()
	uc := &mockTransformerUseCase{}
	uc.submitFunc = func(_ context.Context, req usecase.SubmitConversionRequest) (usecase.SubmitConversionResult, error) {
		uc.lastSubmit = req
		return usecase.SubmitConversionResult{Task: &transformations.TransformationTask{ID: queueID, Status: transformations.StatusPending}}, nil
	}

	client, cleanup := startTestClient(t, uc)
	t.Cleanup(cleanup)

	resp, err := client.ConvertDocumentToMarkdown(context.Background(), &transformerv1.ConvertDocumentToMarkdownRequest{
		DocumentId:        "doc-req",
		OriginalBucket:    "bucket",
		OriginalObjectKey: "path/file.docx",
		CorrelationId:     "corr-from-request",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if uc.lastSubmit.CorrelationID != "corr-from-request" {
		t.Fatalf("expected correlation from request, got %s", uc.lastSubmit.CorrelationID)
	}

	if resp.GetCorrelationId() != "corr-from-request" {
		t.Fatalf("response correlation id mismatch: %s", resp.GetCorrelationId())
	}
}

func TestConvertDocumentToMarkdownWaitsForResult(t *testing.T) {
	queueID := uuid.New()
	doneTask := &transformations.TransformationTask{ID: queueID, Status: transformations.StatusDone, MarkdownBucket: "md-bucket", MarkdownObjectKey: "ready.md"}

	uc := &mockTransformerUseCase{}
	uc.submitFunc = func(_ context.Context, req usecase.SubmitConversionRequest) (usecase.SubmitConversionResult, error) {
		uc.lastSubmit = req
		return usecase.SubmitConversionResult{
			Task:              &transformations.TransformationTask{ID: queueID, Status: transformations.StatusProcessing, MarkdownBucket: "md-bucket"},
			MarkdownBucket:    "fallback-md",
			MarkdownObjectKey: "generated.md",
		}, nil
	}
	uc.waitFunc = func(_ context.Context, id uuid.UUID) (*transformations.TransformationTask, error) {
		uc.waitCalls++
		if id != queueID {
			t.Fatalf("unexpected wait id: %s", id)
		}
		return doneTask, nil
	}

	client, cleanup := startTestClient(t, uc)
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

	if resp.GetStatus() != string(transformations.StatusDone) {
		t.Fatalf("expected done status, got %s", resp.GetStatus())
	}
	if resp.GetMarkdownObjectKey() != "ready.md" {
		t.Fatalf("expected ready key, got %s", resp.GetMarkdownObjectKey())
	}
	if uc.waitCalls != 1 {
		t.Fatalf("expected wait to be called once, got %d", uc.waitCalls)
	}
}

func TestConvertDocumentToMarkdownValidation(t *testing.T) {
	queueID := uuid.New()
	uc := &mockTransformerUseCase{}
	uc.submitFunc = func(_ context.Context, req usecase.SubmitConversionRequest) (usecase.SubmitConversionResult, error) {
		uc.lastSubmit = req
		switch {
		case req.DocumentID == "" || req.OriginalBucket == "" || req.OriginalObjectKey == "":
			return usecase.SubmitConversionResult{}, usecase.ErrInvalidArgument
		case req.DocumentID == "doc-queue-limit":
			return usecase.SubmitConversionResult{}, usecase.ErrQueueLimitReached
		default:
			return usecase.SubmitConversionResult{Task: &transformations.TransformationTask{ID: queueID, Status: transformations.StatusPending}}, nil
		}
	}

	client, cleanup := startTestClient(t, uc)
	t.Cleanup(cleanup)

	_, err := client.ConvertDocumentToMarkdown(context.Background(), &transformerv1.ConvertDocumentToMarkdownRequest{})
if status.Code(err) != codes.InvalidArgument {
t.Fatalf("expected invalid argument, got %v", err)
}

_, err = client.ConvertDocumentToMarkdown(context.Background(), &transformerv1.ConvertDocumentToMarkdownRequest{
DocumentId:        "doc-source-conflict",
OriginalBucket:    "bucket",
OriginalObjectKey: "file.docx",
SourceBucket:      "source",
SourceUrl:         "https://example.com/file.docx",
})

if status.Code(err) != codes.InvalidArgument {
t.Fatalf("expected invalid argument for conflicting sources, got %v", err)
}

_, err = client.ConvertDocumentToMarkdown(context.Background(), &transformerv1.ConvertDocumentToMarkdownRequest{
DocumentId:        "doc-source-missing",
OriginalBucket:    "bucket",
OriginalObjectKey: "file.docx",
SourceBucket:      "source-only",
})

if status.Code(err) != codes.InvalidArgument {
t.Fatalf("expected invalid argument for incomplete source, got %v", err)
}

resp, err := client.ConvertDocumentToMarkdown(context.Background(), &transformerv1.ConvertDocumentToMarkdownRequest{
DocumentId:        "doc-success",
OriginalBucket:    "bucket",
OriginalObjectKey: "file.docx",
SourceBucket:      "source",
SourceObjectKey:   "incoming/file.docx",
})
if err != nil {
t.Fatalf("unexpected error: %v", err)
}
if resp.GetQueueId() == "" {
t.Fatalf("expected queue id in response")
}
if uc.lastSubmit.SourceBucket != "source" || uc.lastSubmit.SourceObjectKey != "incoming/file.docx" {
t.Fatalf("source fields were not propagated: %+v", uc.lastSubmit)
}

_, err = client.ConvertDocumentToMarkdown(context.Background(), &transformerv1.ConvertDocumentToMarkdownRequest{
DocumentId:        "doc-queue-limit",
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

	var receivedFilename string
	var receivedContent []byte

	uc := &mockTransformerUseCase{}
	uc.fetchFunc = func(_ context.Context, url string) ([]byte, error) {
		return docxContent, nil
	}
	uc.convertFunc = func(_ context.Context, filename string, content []byte) (domain.ConvertResult, error) {
		receivedFilename = filename
		receivedContent = content
		return domain.ConvertResult{Content: []byte("# title"), MediaType: "text/markdown", Charset: "utf-8"}, nil
	}

	client, cleanup := startTestClient(t, uc)
	t.Cleanup(cleanup)

	resp, err := client.ConvertDocxToMarkdown(context.Background(), &transformerv1.ConvertDocxToMarkdownRequest{Source: &transformerv1.ConvertDocxToMarkdownRequest_DocxUrl{DocxUrl: ts.URL + "/file.docx"}})
	if err != nil {
		t.Fatalf("call failed: %v", err)
	}

	if resp.GetMarkdown() != "# title" {
		t.Fatalf("unexpected markdown: %s", resp.GetMarkdown())
	}

	if resp.GetCorrelationId() == "" {
		t.Fatalf("expected correlation id in response")
	}
	if string(receivedContent) != string(docxContent) {
		t.Fatalf("converter did not receive docx content")
	}
	if receivedFilename != "file.docx" {
		t.Fatalf("filename not derived from url: %s", receivedFilename)
	}
}

func startTestClient(t *testing.T, uc TransformerUseCase) (transformerv1.TransformerServiceClient, func()) {
	t.Helper()

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

type mockTransformerUseCase struct {
	submitFunc  func(ctx context.Context, req usecase.SubmitConversionRequest) (usecase.SubmitConversionResult, error)
	waitFunc    func(ctx context.Context, queueID uuid.UUID) (*transformations.TransformationTask, error)
	convertFunc func(ctx context.Context, filename string, content []byte) (domain.ConvertResult, error)
	fetchFunc   func(ctx context.Context, rawURL string) ([]byte, error)

	lastSubmit usecase.SubmitConversionRequest
	waitCalls  int
}

func (m *mockTransformerUseCase) SubmitConversion(ctx context.Context, req usecase.SubmitConversionRequest) (usecase.SubmitConversionResult, error) {
	if m.submitFunc == nil {
		return usecase.SubmitConversionResult{}, errors.New("submitFunc not set")
	}
	return m.submitFunc(ctx, req)
}

func (m *mockTransformerUseCase) WaitForResult(ctx context.Context, queueID uuid.UUID) (*transformations.TransformationTask, error) {
	if m.waitFunc == nil {
		return nil, errors.New("waitFunc not set")
	}
	return m.waitFunc(ctx, queueID)
}

func (m *mockTransformerUseCase) ConvertDocxContent(ctx context.Context, filename string, content []byte) (domain.ConvertResult, error) {
	if m.convertFunc == nil {
		return domain.ConvertResult{}, errors.New("convertFunc not set")
	}
	return m.convertFunc(ctx, filename, content)
}

func (m *mockTransformerUseCase) FetchContentByURL(ctx context.Context, rawURL string) ([]byte, error) {
	if m.fetchFunc == nil {
		return nil, errors.New("fetchFunc not set")
	}
	return m.fetchFunc(ctx, rawURL)
}
