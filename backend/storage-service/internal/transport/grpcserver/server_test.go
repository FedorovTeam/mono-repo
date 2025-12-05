package grpcserver

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/metadata"

	"backend-storage-service/internal/storage"
	storagev1 "backend-storage-service/proto/backend/storage/v1"
)

func TestGenerateUploadURLBuildsKeyAndTTL(t *testing.T) {
	client := &mockPresignClient{bucketExists: true}
	generator := storage.NewObjectKeyGenerator(func() time.Time { return time.Date(2025, time.January, 2, 0, 0, 0, 0, time.UTC) })
	server := New(client, generator, "knowledge-docx", storage.NewService(client, generator, "knowledge-docx", "knowledge-md"))

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
	server := New(client, generator, "knowledge-docx", storage.NewService(client, generator, "knowledge-docx", "knowledge-md"))

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

func TestUploadOriginalStoresContent(t *testing.T) {
	client := newMockClient("knowledge-docx", "knowledge-md")
	generator := storage.NewObjectKeyGenerator(func() time.Time { return time.Date(2025, time.January, 2, 0, 0, 0, 0, time.UTC) })
	service := storage.NewService(client, generator, "knowledge-docx", "knowledge-md")
	server := New(client, generator, "knowledge-docx", service)

	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("x-correlation-id", "corr-123"))
	resp, err := server.UploadOriginal(ctx, &storagev1.UploadOriginalRequest{
		OrgSlug:           "acme",
		ProjectSlug:       "knowledge",
		DocumentId:        "doc-123",
		FilenameExtension: "docx",
		Content:           []byte("payload"),
	})
	require.NoError(t, err)
	require.Equal(t, "knowledge-docx", resp.GetBucket())
	require.Equal(t, []byte("payload"), client.storedData[resp.GetBucket()][resp.GetObjectKey()])
	require.Contains(t, resp.GetObjectKey(), "org/acme/project/knowledge/year=2025/month=01/day=02/doc-123.docx")
}

func TestUploadMarkdownUsesDefaultsFromServer(t *testing.T) {
	client := newMockClient("knowledge-docx", "knowledge-md")
	generator := storage.NewObjectKeyGenerator(func() time.Time { return time.Date(2024, time.November, 5, 0, 0, 0, 0, time.UTC) })
	service := storage.NewService(client, generator, "knowledge-docx", "knowledge-md")
	server := New(client, generator, "knowledge-docx", service)

	resp, err := server.UploadMarkdown(context.Background(), &storagev1.UploadMarkdownRequest{
		DocumentId: "doc-md",
		Content:    []byte("markdown"),
	})
	require.NoError(t, err)
	require.Equal(t, "knowledge-md", resp.GetBucket())
	require.Equal(t, []byte("markdown"), client.storedData[resp.GetBucket()][resp.GetObjectKey()])
	require.Contains(t, resp.GetObjectKey(), "org/knowledge/project/documents/year=2024/month=11/day=05/doc-md.md")
}

func TestGetObjectReturnsStoredData(t *testing.T) {
	client := newMockClient("knowledge-docx", "knowledge-md")
	generator := storage.NewObjectKeyGenerator(func() time.Time { return time.Date(2024, time.March, 10, 0, 0, 0, 0, time.UTC) })
	service := storage.NewService(client, generator, "knowledge-docx", "knowledge-md")
	server := New(client, generator, "knowledge-docx", service)

	location, err := service.UploadMarkdown(context.Background(), "org", "project", "doc-get", []byte("content"))
	require.NoError(t, err)

	resp, err := server.GetObject(context.Background(), &storagev1.GetObjectRequest{
		Bucket:    location.Bucket,
		ObjectKey: location.Key,
	})
	require.NoError(t, err)
	require.Equal(t, []byte("content"), resp.GetContent())
}

type mockPresignClient struct {
	bucketExists bool
	lastExpires  time.Duration
	lastHeaders  http.Header
	lastObject   string
	storedData   map[string]map[string][]byte
}

func (m *mockPresignClient) BucketExists(_ context.Context, _ string) (bool, error) {
	return m.bucketExists, nil
}

func (m *mockPresignClient) MakeBucket(context.Context, string, minio.MakeBucketOptions) error {
	return errors.New("not implemented")
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

type mockClient struct {
	buckets    map[string]bool
	storedData map[string]map[string][]byte
}

func newMockClient(buckets ...string) *mockClient {
	available := make(map[string]bool)
	for _, b := range buckets {
		available[b] = true
	}

	return &mockClient{buckets: available, storedData: make(map[string]map[string][]byte)}
}

func (m *mockClient) BucketExists(_ context.Context, bucketName string) (bool, error) {
	return m.buckets[bucketName], nil
}

func (m *mockClient) MakeBucket(_ context.Context, bucketName string, _ minio.MakeBucketOptions) error {
	m.buckets[bucketName] = true
	return nil
}

func (m *mockClient) PutObject(_ context.Context, bucketName, objectName string, reader io.Reader, objectSize int64, _ minio.PutObjectOptions) error {
	if !m.buckets[bucketName] {
		return minio.ErrorResponse{Code: "NoSuchBucket"}
	}

	buf := make([]byte, objectSize)
	if _, err := reader.Read(buf); err != nil {
		return err
	}

	if _, ok := m.storedData[bucketName]; !ok {
		m.storedData[bucketName] = make(map[string][]byte)
	}

	m.storedData[bucketName][objectName] = buf
	return nil
}

func (m *mockClient) GetObject(_ context.Context, bucketName, objectName string) (io.ReadCloser, error) {
	bucketData, ok := m.storedData[bucketName]
	if !ok {
		return nil, minio.ErrorResponse{Code: "NoSuchBucket"}
	}

	data, ok := bucketData[objectName]
	if !ok {
		return nil, minio.ErrorResponse{Code: "NoSuchKey"}
	}

	return io.NopCloser(bytes.NewReader(data)), nil
}

func (m *mockClient) PresignHeader(_ context.Context, _ string, _ string, _ string, _ time.Duration, _ url.Values, _ http.Header) (*url.URL, error) {
	return nil, errors.New("not implemented")
}
