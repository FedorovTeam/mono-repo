package grpcserver

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/minio/minio-go/v7"
	"google.golang.org/grpc/metadata"

	"backend-storage-service/internal/storage"
	storagev1 "backend-storage-service/proto/backend/storage/v1"
)

func TestGenerateUploadURLBuildsKeyAndTTL(t *testing.T) {
	client := &mockPresignClient{bucketExists: true}
	generator := storage.NewObjectKeyGenerator(func() time.Time { return time.Date(2025, time.January, 2, 0, 0, 0, 0, time.UTC) })
	server := New(client, generator, "knowledge-docx")

	resp, err := server.GenerateUploadURL(context.Background(), &storagev1.GenerateUploadURLRequest{
		DocumentId:       "doc-123",
		FileName:         "demo.docx",
		ContentLength:    1024,
		CorrelationId:    "corr-1",
		ExpiresInSeconds: 600,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	expectedKey := "org/knowledge/project/documents/year=2025/month=01/day=02/doc-123.docx"
	if resp.ObjectKey != expectedKey {
		t.Fatalf("unexpected key: %s", resp.ObjectKey)
	}
	if resp.Bucket != "knowledge-docx" {
		t.Fatalf("unexpected bucket: %s", resp.Bucket)
	}
	if resp.ExpiresInSeconds != 600 {
		t.Fatalf("unexpected expires: %d", resp.ExpiresInSeconds)
	}
	if client.lastExpires != 600*time.Second {
		t.Fatalf("expires not propagated to presign: %s", client.lastExpires)
	}
	if got := client.lastHeaders.Get("x-amz-meta-correlation-id"); got != "corr-1" {
		t.Fatalf("correlation id not included: %s", got)
	}
	if client.lastObject != expectedKey {
		t.Fatalf("object key not passed to minio: %s", client.lastObject)
	}
}

func TestGenerateUploadURLUsesMetadataCorrelationID(t *testing.T) {
	client := &mockPresignClient{bucketExists: true}
	generator := storage.NewObjectKeyGenerator(func() time.Time { return time.Date(2024, time.December, 31, 0, 0, 0, 0, time.UTC) })
	server := New(client, generator, "knowledge-docx")

	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("x-correlation-id", "corr-meta"))
	resp, err := server.GenerateUploadURL(ctx, &storagev1.GenerateUploadURLRequest{
		DocumentId:       "doc-999",
		FileName:         "attach.docx",
		ContentLength:    2048,
		ExpiresInSeconds: 900,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if resp == nil || resp.UploadUrl == "" {
		t.Fatal("expected response with upload url")
	}
	if got := client.lastHeaders.Get("x-amz-meta-correlation-id"); got != "corr-meta" {
		t.Fatalf("metadata correlation id not propagated: %s", got)
	}
}

type mockPresignClient struct {
	bucketExists bool
	lastExpires  time.Duration
	lastHeaders  http.Header
	lastObject   string
}

func (m *mockPresignClient) BucketExists(_ context.Context, _ string) (bool, error) {
	return m.bucketExists, nil
}

func (m *mockPresignClient) PutObject(context.Context, string, string, io.Reader, int64, minio.PutObjectOptions) error {
	return errors.New("not implemented")
}

func (m *mockPresignClient) GetObject(context.Context, string, string) (io.ReadCloser, error) {
	return nil, errors.New("not implemented")
}

func (m *mockPresignClient) PresignHeader(_ context.Context, method, bucketName, objectName string, expires time.Duration, _ url.Values, headers http.Header) (*url.URL, error) {
	m.lastExpires = expires
	m.lastHeaders = headers
	m.lastObject = objectName

	return url.Parse("http://minio/upload")
}
