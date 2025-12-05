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
	transformerv1 "github.com/mono-repo/docling-transformer-service/proto/backend/transformer/v1"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/test/bufconn"
)

const bufSize = 1024 * 1024

func TestE2EConversionAgainstDoclingStub(t *testing.T) {
	var receivedBody []byte
	docling := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		receivedBody = body
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("## converted by docling"))
	}))
	defer docling.Close()

	conv := converter.NewDoclingConverter(docling.URL)
	logger := zap.NewNop()

	lis := bufconn.Listen(bufSize)
	srv := grpc.NewServer(grpc.UnaryInterceptor(server.LoggingInterceptor(logger)))
	transformerv1.RegisterTransformerServiceServer(srv, server.NewTransformerAPI(conv, logger))

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
	resp, err := client.ConvertDocxToMarkdown(ctx, &transformerv1.ConvertDocxToMarkdownRequest{DocxContent: []byte("docx stub")})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if string(receivedBody) != "docx stub" {
		t.Fatalf("unexpected body sent to docling: %s", string(receivedBody))
	}

	if resp.GetMarkdown() != "## converted by docling" {
		t.Fatalf("unexpected markdown: %s", resp.GetMarkdown())
	}
}
