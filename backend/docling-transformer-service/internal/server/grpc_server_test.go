package server

import (
	"context"
	"net"
	"testing"

	"github.com/mono-repo/docling-transformer-service/internal/config"
	"github.com/mono-repo/docling-transformer-service/internal/converter"
	"github.com/mono-repo/docling-transformer-service/internal/telemetry"
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

type fakeConverter struct {
	response converter.DoclingConvertResult
	err      error
	ctx      context.Context
	request  converter.DoclingConvertRequest
}

func (f *fakeConverter) Convert(ctx context.Context, req converter.DoclingConvertRequest) (converter.DoclingConvertResult, error) {
	f.ctx = ctx
	f.request = req
	return f.response, f.err
}

type fakeStorage struct {
	downloadContent []byte
	downloadErr     error
	uploadErr       error

	downloadedBucket string
	downloadedKey    string
	uploadedBucket   string
	uploadedKey      string
	uploadedType     string
	uploadedContent  []byte
}

func (f *fakeStorage) Download(_ context.Context, bucket, objectKey string) ([]byte, error) {
	f.downloadedBucket = bucket
	f.downloadedKey = objectKey
	if f.downloadErr != nil {
		return nil, f.downloadErr
	}
	return f.downloadContent, nil
}

func (f *fakeStorage) Upload(_ context.Context, bucket, objectKey, contentType string, content []byte) error {
	f.uploadedBucket = bucket
	f.uploadedKey = objectKey
	f.uploadedType = contentType
	f.uploadedContent = content
	return f.uploadErr
}

func TestConvertDocumentToMarkdown(t *testing.T) {
	conv := &fakeConverter{response: converter.DoclingConvertResult{Content: []byte("markdown"), MediaType: "text/markdown"}}
	storage := &fakeStorage{downloadContent: []byte("docx bytes")}
	logger := zap.NewNop()

	lis := bufconn.Listen(bufSize)
	srv := newGRPCServer(logger, config.ObservabilityConfig{LoggingEnabled: true})
	transformerv1.RegisterTransformerServiceServer(srv, NewTransformerAPI(conv, storage, "markdown-bucket", logger))

	go func() {
		_ = srv.Serve(lis)
	}()
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
		MarkdownBucket:    "markdown-bucket",
		MarkdownObjectKey: "md/file.md",
		FilenameExtension: ".docx",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if string(resp.GetMarkdownContent()) != "markdown" {
		t.Fatalf("unexpected markdown: %s", string(resp.GetMarkdownContent()))
	}

	if resp.GetMediaType() != "text/markdown" {
		t.Fatalf("unexpected media type: %s", resp.GetMediaType())
	}

	if storage.downloadedBucket != "bucket-orig" || storage.downloadedKey != "path/file.docx" {
		t.Fatalf("unexpected download args: %s %s", storage.downloadedBucket, storage.downloadedKey)
	}

	if storage.uploadedBucket != "markdown-bucket" || storage.uploadedKey != "md/file.md" {
		t.Fatalf("unexpected upload args: %s %s", storage.uploadedBucket, storage.uploadedKey)
	}

	corrID := telemetry.CorrelationIDFromContext(conv.ctx)
	if corrID != "corr-123" {
		t.Fatalf("correlation id not propagated, got %s", corrID)
	}

	if conv.request.ContentType != "application/vnd.openxmlformats-officedocument.wordprocessingml.document" {
		t.Fatalf("unexpected content type: %s", conv.request.ContentType)
	}

	if conv.request.FileName != "file.docx" {
		t.Fatalf("unexpected filename: %s", conv.request.FileName)
	}
}

func TestConvertDocumentToMarkdownValidation(t *testing.T) {
	conv := &fakeConverter{response: converter.DoclingConvertResult{Content: []byte("markdown")}}
	storage := &fakeStorage{downloadContent: []byte("docx bytes")}
	logger := zap.NewNop()

	lis := bufconn.Listen(bufSize)
	srv := newGRPCServer(logger, config.ObservabilityConfig{LoggingEnabled: true})
	transformerv1.RegisterTransformerServiceServer(srv, NewTransformerAPI(conv, storage, "md", logger))

	go func() {
		_ = srv.Serve(lis)
	}()
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

	_, err = client.ConvertDocumentToMarkdown(context.Background(), &transformerv1.ConvertDocumentToMarkdownRequest{
		DocumentId:        "doc-123",
		OriginalBucket:    "bucket",
		OriginalObjectKey: "file.txt",
		MarkdownBucket:    "md",
	})

	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("expected invalid argument for unsupported extension, got %v", err)
	}
}
