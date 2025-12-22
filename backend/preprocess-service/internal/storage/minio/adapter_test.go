package minio

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/minio/minio-go/v7"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

type fakeMinioClient struct {
	downloadErr error
	uploadErr   error
}

func (f *fakeMinioClient) FGetObject(_ context.Context, _, _, _ string, _ minio.GetObjectOptions) error {
	return f.downloadErr
}

func (f *fakeMinioClient) FPutObject(_ context.Context, _, _, _ string, _ minio.PutObjectOptions) (minio.UploadInfo, error) {
	return minio.UploadInfo{}, f.uploadErr
}

func TestAdapterDownloadLogs(t *testing.T) {
	t.Helper()

	core, logs := observer.New(zap.InfoLevel)
	adapter := NewAdapter(&fakeMinioClient{}, zap.New(core))

	destPath := filepath.Join(t.TempDir(), "destination")
	if err := adapter.Download(context.Background(), "bucket", "key", "etag", destPath); err != nil {
		t.Fatalf("download returned error: %v", err)
	}

	if logs.Len() == 0 {
		t.Fatalf("expected logger to be called during download")
	}
}

func TestAdapterUploadLogs(t *testing.T) {
	t.Helper()

	core, logs := observer.New(zap.InfoLevel)
	adapter := NewAdapter(&fakeMinioClient{}, zap.New(core))

	sourcePath := filepath.Join(t.TempDir(), "source")
	if err := adapter.Upload(context.Background(), "bucket", "key", sourcePath, "application/octet-stream"); err != nil {
		t.Fatalf("upload returned error: %v", err)
	}

	if logs.Len() == 0 {
		t.Fatalf("expected logger to be called during upload")
	}
}
