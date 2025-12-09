package server

import (
	"context"
	"net"
	"testing"

	"github.com/google/uuid"
	"github.com/mono-repo/docling-transformer-service/internal/config"
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
	active       int
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
	return f.active, nil
}

func (*fakeQueueRepo) AcquireNext(context.Context) (*repository.TransformationTask, error) {
	return nil, nil
}
func (*fakeQueueRepo) MarkDone(context.Context, uuid.UUID, string, string) error { return nil }
func (*fakeQueueRepo) MarkFailed(context.Context, uuid.UUID, string) error       { return nil }

func TestConvertDocumentToMarkdownQueuesTask(t *testing.T) {
	repo := &fakeQueueRepo{queueID: uuid.New()}
	logger := zap.NewNop()

	lis := bufconn.Listen(bufSize)
	srv := newGRPCServer(logger, config.ObservabilityConfig{LoggingEnabled: true})
	transformerv1.RegisterTransformerServiceServer(srv, NewTransformerAPI(repo, "markdown-bucket", 2, logger))

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
}

func TestConvertDocumentToMarkdownValidation(t *testing.T) {
	repo := &fakeQueueRepo{}
	logger := zap.NewNop()

	lis := bufconn.Listen(bufSize)
	srv := newGRPCServer(logger, config.ObservabilityConfig{})
	transformerv1.RegisterTransformerServiceServer(srv, NewTransformerAPI(repo, "md", 1, logger))

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

	repo.active = 2
	_, err = client.ConvertDocumentToMarkdown(context.Background(), &transformerv1.ConvertDocumentToMarkdownRequest{
		DocumentId:        "doc-123",
		OriginalBucket:    "bucket",
		OriginalObjectKey: "file.docx",
	})

	if status.Code(err) != codes.ResourceExhausted {
		t.Fatalf("expected resource exhausted, got %v", err)
	}
}
