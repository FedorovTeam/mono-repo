package object

import (
	"context"
	"io"
	"time"

	"github.com/minio/minio-go/v7"
	"go.uber.org/zap"

	"github.com/mono-repo/table-extractor-service/internal/config"
	"github.com/mono-repo/table-extractor-service/internal/telemetry"
	"github.com/mono-repo/table-extractor-service/internal/usecase"
)

type MinioStorage struct {
	client           *minio.Client
	operationTimeout time.Duration
	logger           *zap.Logger
}

func NewMinioStorage(client *minio.Client, timeouts config.StorageConfig, logger *zap.Logger) *MinioStorage {
	timeout := timeouts.OperationTimeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	if logger == nil {
		logger = zap.NewNop()
	}
	return &MinioStorage{
		client:           client,
		operationTimeout: timeout,
		logger:           logger,
	}
}

func (s *MinioStorage) Download(ctx context.Context, bucket, objectKey string) (io.ReadCloser, error) {
	opCtx, cancel := s.withTimeout(ctx)

	fields := s.baseFields(ctx, bucket, objectKey)
	s.logger.Debug("start downloading object", fields...)
	reader, err := s.client.GetObject(opCtx, bucket, objectKey, minio.GetObjectOptions{})
	if err != nil {
		cancel()
		s.logger.Error("failed to download object", append(fields, zap.Error(err))...)
		return nil, err
	}

	s.logger.Debug("object download initiated", fields...)
	return &cancelOnCloseReader{ReadCloser: reader, cancel: cancel}, nil
}

func (s *MinioStorage) Upload(ctx context.Context, bucket, objectKey, contentType string, content io.Reader, size int64) error {
	opCtx, cancel := s.withTimeout(ctx)
	defer cancel()

	fields := append(s.baseFields(ctx, bucket, objectKey), zap.Int64("size_bytes", size))
	s.logger.Debug("start uploading object", fields...)
	_, err := s.client.PutObject(opCtx, bucket, objectKey, content, size, minio.PutObjectOptions{
		ContentType: contentType,
	})
	if err != nil {
		s.logger.Error("failed to upload object", append(fields, zap.Error(err))...)
		return err
	}

	s.logger.Debug("object uploaded", fields...)
	return err
}

var _ usecase.StorageClient = (*MinioStorage)(nil)

func (s *MinioStorage) withTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, s.operationTimeout)
}

func (s *MinioStorage) baseFields(ctx context.Context, bucket, objectKey string) []zap.Field {
	return []zap.Field{
		zap.String("bucket", bucket),
		zap.String("object_key", objectKey),
		zap.String("correlation_id", telemetry.CorrelationIDFromContext(ctx)),
	}
}

type cancelOnCloseReader struct {
	io.ReadCloser
	cancel context.CancelFunc
}

func (r *cancelOnCloseReader) Close() error {
	defer r.cancel()
	return r.ReadCloser.Close()
}
