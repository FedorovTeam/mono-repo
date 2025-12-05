package storage

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/stretchr/testify/require"
)

type mockClient struct {
	buckets     map[string]bool
	storedData  map[string]map[string][]byte
	lastPutOpts minio.PutObjectOptions
	lastPresign struct {
		method  string
		bucket  string
		object  string
		expires time.Duration
		headers http.Header
	}
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

func (m *mockClient) PutObject(_ context.Context, bucketName, objectName string, reader io.Reader, objectSize int64, opts minio.PutObjectOptions) error {
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
	m.lastPutOpts = opts
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

func (m *mockClient) PresignHeader(_ context.Context, method, bucketName, objectName string, expires time.Duration, _ url.Values, headers http.Header) (*url.URL, error) {
	m.lastPresign.method = method
	m.lastPresign.bucket = bucketName
	m.lastPresign.object = objectName
	m.lastPresign.expires = expires
	m.lastPresign.headers = headers

	return url.Parse("http://example.com/presigned")
}

func TestObjectKeyGenerator_BuildKey(t *testing.T) {
	now := func() time.Time {
		return time.Date(2025, time.January, 2, 3, 4, 5, 0, time.UTC)
	}

	generator := NewObjectKeyGenerator(now)
	key, err := generator.BuildKey("acme", "knowledge", "doc-42", ".docx")
	require.NoError(t, err)
	require.Equal(t, "org/acme/project/knowledge/year=2025/month=01/day=02/doc-42.docx", key)
}

func TestService_UploadAndDownload(t *testing.T) {
	client := newMockClient("knowledge-docx", "knowledge-md")
	generator := NewObjectKeyGenerator(func() time.Time { return time.Date(2024, 12, 31, 0, 0, 0, 0, time.UTC) })
	svc := NewService(client, generator, "knowledge-docx", "knowledge-md")

	ctx := context.Background()
	originalContent := []byte("original-bytes")

	originalLocation, err := svc.UploadOriginal(ctx, "org", "proj", "id-1", "docx", originalContent)
	require.NoError(t, err)

	require.Contains(t, originalLocation.Key, "org/org/project/proj/year=2024/month=12/day=31/id-1.docx")
	require.Equal(t, "knowledge-docx", originalLocation.Bucket)
	require.Equal(t, "application/vnd.openxmlformats-officedocument.wordprocessingml.document", client.lastPutOpts.ContentType)

	markdownContent := []byte("markdown")
	mdLocation, err := svc.UploadMarkdown(ctx, "org", "proj", "id-1", markdownContent)
	require.NoError(t, err)

	require.Contains(t, mdLocation.Key, "org/org/project/proj/year=2024/month=12/day=31/id-1.md")
	require.Equal(t, "knowledge-md", mdLocation.Bucket)
	require.Equal(t, "text/markdown", client.lastPutOpts.ContentType)

	downloaded, err := svc.DownloadObject(ctx, mdLocation.Bucket, mdLocation.Key)
	require.NoError(t, err)
	require.Equal(t, markdownContent, downloaded)
}

func TestService_ErrorsOnMissingBucket(t *testing.T) {
	client := newMockClient("knowledge-docx")
	generator := NewObjectKeyGenerator(time.Now)
	svc := NewService(client, generator, "knowledge-docx", "knowledge-md")

	_, err := svc.UploadMarkdown(context.Background(), "org", "proj", "id-1", []byte("md"))
	require.Error(t, err)
}
