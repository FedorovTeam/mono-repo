package storage

import (
	"context"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	gofakes3 "github.com/johannesboyne/gofakes3"
	"github.com/johannesboyne/gofakes3/backend/s3mem"
	"github.com/stretchr/testify/require"
)

func TestNewMinioClientInitializesBuckets(t *testing.T) {
	ctx := context.Background()
	endpoint, cleanup := startFakeMinio(t)
	defer cleanup()

	client, err := NewMinioClient(ctx, MinioClientConfig{
		Endpoint:  endpoint,
		AccessKey: "access",
		SecretKey: "secret",
		Buckets:   []string{"knowledge-original", "knowledge-md"},
	})
	require.NoError(t, err)

	generator := NewObjectKeyGenerator(func() time.Time {
		return time.Date(2024, time.July, 1, 0, 0, 0, 0, time.UTC)
	})
	service := NewService(client, generator, "knowledge-original", "knowledge-md", UploadValidationConfig{})

	location, err := service.UploadOriginal(ctx, "org", "proj", "doc-1", "docx", []byte("payload"))
	require.NoError(t, err)

	data, err := service.DownloadObject(ctx, location.Bucket, location.Key)
	require.NoError(t, err)
	require.Equal(t, []byte("payload"), data.Content)
	require.NotEmpty(t, data.ETag)

	existsDocx, err := client.BucketExists(ctx, "knowledge-original")
	require.NoError(t, err)
	require.True(t, existsDocx)

	existsMD, err := client.BucketExists(ctx, "knowledge-md")
	require.NoError(t, err)
	require.True(t, existsMD)
}

func startFakeMinio(t *testing.T) (string, func()) {
	t.Helper()

	backend := s3mem.New()
	server := gofakes3.New(backend)
	httpServer := httptest.NewServer(server.Server())

	endpointURL, err := url.Parse(httpServer.URL)
	require.NoError(t, err)

	return endpointURL.Host, httpServer.Close
}
