package storage

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/notification"
	"github.com/stretchr/testify/require"
)

type mockClient struct {
	buckets     map[string]bool
	storedData  map[string]map[string][]byte
	lastPutOpts minio.PutObjectOptions
	lastStat    struct {
		bucket string
		object string
	}
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

func (m *mockClient) ListenBucketNotification(ctx context.Context, bucketName, prefix, suffix string, events []string) <-chan notification.Info {
	ch := make(chan notification.Info)
	close(ch)
	return ch
}

func (m *mockClient) SetBucketNotification(ctx context.Context, bucketName string, config notification.Configuration) error {
	return nil
}

func (m *mockClient) MakeBucket(_ context.Context, bucketName string, _ minio.MakeBucketOptions) error {
	m.buckets[bucketName] = true
	return nil
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

func (m *mockClient) StatObject(_ context.Context, bucketName, objectName string, _ minio.StatObjectOptions) (minio.ObjectInfo, error) {
	bucketData, ok := m.storedData[bucketName]
	if !ok {
		return minio.ObjectInfo{}, minio.ErrorResponse{Code: "NoSuchBucket"}
	}
	data, ok := bucketData[objectName]
	if !ok {
		return minio.ObjectInfo{}, minio.ErrorResponse{Code: "NoSuchKey"}
	}
	m.lastStat.bucket = bucketName
	m.lastStat.object = objectName
	return minio.ObjectInfo{
		ETag:         "etag-" + objectName,
		Size:         int64(len(data)),
		LastModified: time.Now().UTC(),
	}, nil
}

func (m *mockClient) PresignHeader(_ context.Context, method, bucketName, objectName string, expires time.Duration, _ url.Values, headers http.Header) (*url.URL, error) {
	m.lastPresign.method = method
	m.lastPresign.bucket = bucketName
	m.lastPresign.object = objectName
	m.lastPresign.expires = expires
	m.lastPresign.headers = headers

	signedHeaders := make([]string, 0, len(headers))
	for header := range headers {
		signedHeaders = append(signedHeaders, strings.ToLower(header))
	}
	sort.Strings(signedHeaders)

	u, _ := url.Parse("http://example.com/presigned")
	if len(signedHeaders) > 0 {
		q := u.Query()
		q.Set("X-Amz-SignedHeaders", strings.Join(signedHeaders, ";"))
		u.RawQuery = q.Encode()
	}

	return u, nil
}

func TestObjectKeyGenerator_BuildKey(t *testing.T) {
	now := func() time.Time {
		return time.Date(2025, time.January, 2, 3, 4, 5, 0, time.UTC)
	}

	generator := NewObjectKeyGenerator(now)
	generator.newUUID = func() uuid.UUID {
		return uuid.MustParse("123e4567-e89b-12d3-a456-426614174000")
	}
	key, err := generator.BuildKey("acme", "knowledge", "doc-42", ".docx")
	require.NoError(t, err)
	require.Equal(t, "org/acme/project/knowledge/year=2025/month=01/day=02/123e4567-e89b-12d3-a456-426614174000/doc-42.docx", key)
}

func TestService_UploadAndDownload(t *testing.T) {
	client := newMockClient("knowledge-original", "knowledge-md")
	generator := NewObjectKeyGenerator(func() time.Time { return time.Date(2024, 12, 31, 0, 0, 0, 0, time.UTC) })
	generator.newUUID = func() uuid.UUID {
		return uuid.MustParse("123e4567-e89b-12d3-a456-426614174001")
	}
	svc := NewService(client, generator, "knowledge-original", "knowledge-md", UploadValidationConfig{})

	ctx := context.Background()
	originalContent := []byte("original-bytes")

	originalLocation, err := svc.UploadOriginal(ctx, "org", "proj", "id-1", "docx", originalContent)
	require.NoError(t, err)

	require.Equal(t, "org/org/project/proj/year=2024/month=12/day=31/123e4567-e89b-12d3-a456-426614174001/id-1.docx", originalLocation.Key)
	_, err = uuid.Parse(strings.Split(originalLocation.Key, "/")[7])
	require.NoError(t, err)
	require.Equal(t, "knowledge-original", originalLocation.Bucket)
	require.Equal(t, "application/vnd.openxmlformats-officedocument.wordprocessingml.document", client.lastPutOpts.ContentType)

	markdownContent := []byte("markdown")
	mdLocation, err := svc.UploadMarkdown(ctx, "org", "proj", "id-1", markdownContent)
	require.NoError(t, err)

	require.Equal(t, "org/org/project/proj/year=2024/month=12/day=31/123e4567-e89b-12d3-a456-426614174001/id-1.md", mdLocation.Key)
	_, err = uuid.Parse(strings.Split(mdLocation.Key, "/")[7])
	require.NoError(t, err)
	require.Equal(t, "knowledge-md", mdLocation.Bucket)
	require.Equal(t, "text/markdown", client.lastPutOpts.ContentType)

	downloaded, err := svc.DownloadObject(ctx, mdLocation.Bucket, mdLocation.Key)
	require.NoError(t, err)
	require.Equal(t, markdownContent, downloaded.Content)
	require.NotEmpty(t, downloaded.ETag)
	require.False(t, downloaded.UpdatedAt.IsZero())
}

func TestService_ErrorsOnMissingBucket(t *testing.T) {
	client := newMockClient("knowledge-original")
	generator := NewObjectKeyGenerator(time.Now)
	svc := NewService(client, generator, "knowledge-original", "knowledge-md", UploadValidationConfig{})

	_, err := svc.UploadMarkdown(context.Background(), "org", "proj", "id-1", []byte("md"))
	require.Error(t, err)
}

func TestGenerateUploadURL_ValidatesInput(t *testing.T) {
	client := newMockClient("knowledge-original", "knowledge-md")
	generator := NewObjectKeyGenerator(func() time.Time { return time.Date(2025, time.January, 2, 0, 0, 0, 0, time.UTC) })
	svc := NewService(client, generator, "knowledge-original", "knowledge-md", UploadValidationConfig{MaxSizeBytes: 2048, AllowedExtensions: map[string]struct{}{"docx": {}}, SignedURLTTL: 600})

	ctx := context.Background()

	params := GenerateUploadURLParams{
		OrgSlug:          "org",
		ProjectSlug:      "proj",
		DocumentID:       "doc-1",
		FileName:         "test.docx",
		ContentLength:    1024,
		CorrelationID:    "corr-1",
		ExpiresInSeconds: 600,
		UserMetadata: map[string]string{
			"x-amz-meta-source": "mobile",
		},
	}

	resp, err := svc.GenerateUploadURL(ctx, "knowledge-original", params)
	require.NoError(t, err)
	require.Equal(t, "knowledge-original", resp.Bucket)
	require.Contains(t, resp.ObjectKey, "doc-1.docx")
	require.Equal(t, int64(600), resp.ExpiresInSeconds)
	require.Equal(t, "example.com", resp.UploadURL.Host)
	require.Equal(t, "corr-1", client.lastPresign.headers.Get("x-amz-meta-correlation-id"))
	require.Equal(t, "corr-1", resp.RequiredHeaders["x-amz-meta-correlation-id"])
	require.Equal(t, "mobile", client.lastPresign.headers.Get("x-amz-meta-source"))
	require.Equal(t, "mobile", resp.RequiredHeaders["x-amz-meta-source"])
	require.Contains(t, resp.UploadURL.Query().Get("X-Amz-SignedHeaders"), "x-amz-meta-correlation-id")
	require.Contains(t, resp.UploadURL.Query().Get("X-Amz-SignedHeaders"), "x-amz-meta-source")

	_, err = svc.GenerateUploadURL(ctx, "knowledge-original", GenerateUploadURLParams{})
	require.ErrorIs(t, err, ErrInvalidArgument)

	_, err = svc.GenerateUploadURL(ctx, "knowledge-original", GenerateUploadURLParams{
		DocumentID:       "doc-1",
		FileName:         "unsafe.exe",
		ContentLength:    10,
		ExpiresInSeconds: 10,
	})
	require.ErrorIs(t, err, ErrInvalidArgument)

	_, err = svc.GenerateUploadURL(ctx, "knowledge-original", GenerateUploadURLParams{
		DocumentID:       "doc-1",
		FileName:         "demo.pdf",
		ContentLength:    -1,
		ExpiresInSeconds: 10,
	})
	require.ErrorIs(t, err, ErrInvalidArgument)

	_, err = svc.GenerateUploadURL(ctx, "knowledge-original", GenerateUploadURLParams{
		DocumentID:       "doc-1",
		FileName:         "demo.docx",
		ContentLength:    4096,
		ExpiresInSeconds: 10,
	})
	require.ErrorIs(t, err, ErrInvalidArgument)

	_, err = svc.GenerateUploadURL(ctx, "knowledge-original", GenerateUploadURLParams{
		DocumentID:    "doc-1",
		FileName:      "demo.docx",
		ContentLength: 10,
		UserMetadata: map[string]string{
			"meta-custom": "value",
		},
	})
	require.ErrorIs(t, err, ErrInvalidArgument)

	_, err = svc.GenerateUploadURL(ctx, "knowledge-original", GenerateUploadURLParams{
		DocumentID:    "doc-1",
		FileName:      "demo.docx",
		ContentLength: 10,
		UserMetadata: map[string]string{
			"x-amz-meta-correlation-id": "override",
		},
	})
	require.ErrorIs(t, err, ErrInvalidArgument)
}

func TestGenerateDownloadURL_ValidatesAndPresigns(t *testing.T) {
	client := newMockClient("knowledge-original", "knowledge-md")
	client.storedData["knowledge-original"] = map[string][]byte{
		"doc-key": []byte("payload"),
	}

	generator := NewObjectKeyGenerator(time.Now)
	svc := NewService(client, generator, "knowledge-original", "knowledge-md", UploadValidationConfig{SignedURLTTL: 300})

	resp, err := svc.GenerateDownloadURL(context.Background(), "knowledge-original", "doc-key", 0, "corr-dl")
	require.NoError(t, err)
	require.Equal(t, "example.com", resp.DownloadURL.Host)
	require.Equal(t, int64(300), resp.ExpiresInSeconds)
	require.Equal(t, http.MethodGet, client.lastPresign.method)
	require.Equal(t, 300*time.Second, client.lastPresign.expires)
	require.Equal(t, "corr-dl", client.lastPresign.headers.Get("x-amz-meta-correlation-id"))
	require.Contains(t, resp.DownloadURL.Query().Get("X-Amz-SignedHeaders"), "x-amz-meta-correlation-id")

	_, err = svc.GenerateDownloadURL(context.Background(), "", "doc-key", 0, "")
	require.ErrorIs(t, err, ErrInvalidArgument)

	_, err = svc.GenerateDownloadURL(context.Background(), "knowledge-original", "", 0, "")
	require.ErrorIs(t, err, ErrInvalidArgument)

	_, err = svc.GenerateDownloadURL(context.Background(), "knowledge-original", "missing", 0, "")
	require.Error(t, err)
}
