package object

import (
	"context"
	"io"
	"time"

	"github.com/minio/minio-go/v7"

	"github.com/mono-repo/table-extractor-service/internal/config"
	"github.com/mono-repo/table-extractor-service/internal/usecase"
)

type MinioStorage struct {
	client           *minio.Client
	operationTimeout time.Duration
}

func NewMinioStorage(client *minio.Client, timeouts config.StorageConfig) *MinioStorage {
	timeout := timeouts.OperationTimeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	return &MinioStorage{
		client:           client,
		operationTimeout: timeout,
	}
}

func (s *MinioStorage) Download(ctx context.Context, bucket, objectKey string) (io.ReadCloser, error) {
	opCtx, cancel := s.withTimeout(ctx)
	defer cancel()
	return s.client.GetObject(opCtx, bucket, objectKey, minio.GetObjectOptions{})
}

func (s *MinioStorage) Upload(ctx context.Context, bucket, objectKey, contentType string, content io.Reader, size int64) error {
	opCtx, cancel := s.withTimeout(ctx)
	defer cancel()

	_, err := s.client.PutObject(opCtx, bucket, objectKey, content, size, minio.PutObjectOptions{
		ContentType: contentType,
	})
	return err
}

var _ usecase.StorageClient = (*MinioStorage)(nil)

func (s *MinioStorage) withTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, s.operationTimeout)
}
