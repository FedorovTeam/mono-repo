package integration

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
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

		if r.URL.Path != "/v1/convert/file" {
			t.Fatalf("unexpected Docling path: %s", r.URL.Path)
		}

		if r.Header.Get("X-Correlation-Id") != "integration-1" {
			t.Fatalf("Docling header mismatch: %s", r.Header.Get("X-Correlation-Id"))
		}

		if !strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data") {
			t.Fatalf("Docling content-type mismatch: %s", r.Header.Get("Content-Type"))
		}

		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Fatalf("failed to parse multipart form: %v", err)
		}

		file, header, err := r.FormFile("files")
		if err != nil {
			t.Fatalf("missing files form field: %v", err)
		}
		defer file.Close()

		if header.Filename != "file.docx" {
			t.Fatalf("unexpected source filename: %s", header.Filename)
		}

		body, _ := io.ReadAll(file)
		receivedBody = body
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, `{"document":{"filename":"file.docx","md_content":"## converted by docling"},"status":"success","processing_time":0.2}`)
	}))
	defer docling.Close()

	conv := converter.NewDoclingConverter(docling.URL, "", zap.NewNop())
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
	resp, err := client.ConvertDocumentToMarkdown(ctx, &transformerv1.ConvertDocumentToMarkdownRequest{
		DocumentId:        "doc-1",
		OriginalBucket:    "orig",
		OriginalObjectKey: "path/file.docx",
		MarkdownBucket:    "md",
		MarkdownObjectKey: "dest/file.md",
		FilenameExtension: "docx",
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
