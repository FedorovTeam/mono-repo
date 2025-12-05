package integration

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/mono-repo/docling-transformer-service/internal/converter"
	"github.com/mono-repo/docling-transformer-service/internal/server"
	"github.com/mono-repo/docling-transformer-service/internal/storage"
	transformerv1 "github.com/mono-repo/docling-transformer-service/proto/backend/transformer/v1"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/test/bufconn"
)

const bufSize = 1024 * 1024

type stubStorage struct {
	download []byte
	uploaded []byte
}

var _ storage.ObjectStorage = (*stubStorage)(nil)

func (s *stubStorage) Download(context.Context, string, string) ([]byte, error) {
	return s.download, nil
}

func (s *stubStorage) Upload(_ context.Context, _, _ string, _ string, content []byte) error {
	s.uploaded = content
	return nil
}

func TestE2EConversionAgainstDoclingStub(t *testing.T) {
	var receivedBody []byte
	var docling *httptest.Server
	docling = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("Docling expects POST, got %s", r.Method)
		}

		if r.URL.Path != "/convert" {
			t.Fatalf("unexpected Docling path: %s", r.URL.Path)
		}

		if r.Header.Get("X-Correlation-Id") != "integration-1" {
			t.Fatalf("Docling header mismatch: %s", r.Header.Get("X-Correlation-Id"))
		}

		if r.Header.Get("X-Docling-Endpoint") != docling.URL {
			t.Fatalf("Docling endpoint header missing: %s", r.Header.Get("X-Docling-Endpoint"))
		}

		if r.Header.Get("Content-Type") != "application/vnd.openxmlformats-officedocument.wordprocessingml.document" {
			t.Fatalf("Docling content-type mismatch: %s", r.Header.Get("Content-Type"))
		}

		if source := r.Header.Get("X-Docling-Source-Filename"); source != "file.docx" {
			t.Fatalf("unexpected source filename header: %s", source)
		}

		body, _ := io.ReadAll(r.Body)
		receivedBody = body
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("## converted by docling"))
	}))
	defer docling.Close()

	conv := converter.NewDoclingConverter(docling.URL, zap.NewNop())
	memStorage := &stubStorage{download: []byte("docx stub from storage")}
	logger := zap.NewNop()

	lis := bufconn.Listen(bufSize)
	srv := grpc.NewServer(grpc.UnaryInterceptor(server.LoggingInterceptor(logger)))
	transformerv1.RegisterTransformerServiceServer(srv, server.NewTransformerAPI(conv, memStorage, "md", logger))

	go func() {
		_ = srv.Serve(lis)
	}()
	t.Cleanup(srv.Stop)

	ctx := metadata.NewOutgoingContext(context.Background(), metadata.Pairs("x-correlation-id", "integration-1"))
	conn, err := grpc.DialContext(ctx, "bufnet", grpc.WithContextDialer(func(_ context.Context, s string) (net.Conn, error) {
		return lis.Dial()
	}), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("failed to dial bufnet: %v", err)
	}
	t.Cleanup(func() { conn.Close() })

	client := transformerv1.NewTransformerServiceClient(conn)
	resp, err := client.ConvertDocxToMarkdown(ctx, &transformerv1.ConvertDocxToMarkdownRequest{
		DocumentId:        "doc-1",
		OriginalBucket:    "orig",
		OriginalObjectKey: "path/file.docx",
		MarkdownBucket:    "md",
		MarkdownObjectKey: "dest/file.md",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if string(receivedBody) != "docx stub from storage" {
		t.Fatalf("unexpected body sent to docling: %s", string(receivedBody))
	}

	if string(resp.GetMarkdownContent()) != "## converted by docling" {
		t.Fatalf("unexpected markdown: %s", string(resp.GetMarkdownContent()))
	}

	if string(memStorage.uploaded) != "## converted by docling" {
		t.Fatalf("markdown was not uploaded to storage")
	}
}
