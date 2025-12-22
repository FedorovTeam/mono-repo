package minio

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"go.uber.org/zap"

	"preprocess-service/internal/config"
	"preprocess-service/internal/usecase/ports"
)

type Adapter struct {
	client Client
	logger *zap.Logger
}

type Client interface {
	FGetObject(ctx context.Context, bucketName, objectName, filePath string, opts minio.GetObjectOptions) error
	FPutObject(ctx context.Context, bucketName, objectName, filePath string, opts minio.PutObjectOptions) (minio.UploadInfo, error)
}

func NewClient(cfg config.MinioConfig) (*minio.Client, error) {
	if cfg.Endpoint == "" {
		return nil, fmt.Errorf("minio endpoint is required")
	}

	return minio.New(cfg.Endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(cfg.AccessKey, cfg.SecretKey, ""),
		Secure: cfg.SSL,
		Region: cfg.Region,
	})
}

func NewAdapter(client Client, logger *zap.Logger) *Adapter {
	if logger == nil {
		logger = zap.NewNop()
	}

	return &Adapter{
		client: client,
		logger: logger,
	}
}

func (a *Adapter) Download(ctx context.Context, bucket, key, etag, destinationPath string) error {
	start := time.Now()
	logger := a.logger.With(
		zap.String("bucket", bucket),
		zap.String("key", key),
		zap.String("etag", etag),
		zap.String("destination_path", destinationPath),
	)
	logger.Info("starting storage download")

	opts := minio.GetObjectOptions{}
	if etag != "" {
		if err := opts.SetMatchETag(etag); err != nil {
			logger.Error("failed to set etag", zap.Duration("duration", time.Since(start)), zap.Error(err))
			return fmt.Errorf("set etag: %w", err)
		}
	}

	if err := a.client.FGetObject(ctx, bucket, key, destinationPath, opts); err != nil {
		var resp minio.ErrorResponse
		statusCode := statusCodeFromError(err)
		logger.Error("storage download failed",
			zap.Duration("duration", time.Since(start)),
			zap.String("status", "error"),
			zap.Int("status_code", statusCode),
			zap.Error(err),
		)
		if errors.As(err, &resp) {
			switch resp.StatusCode {
			case 404:
				return fmt.Errorf("%w: %v", ports.ErrObjectNotFound, err)
			case 412:
				return fmt.Errorf("%w: %v", ports.ErrPreconditionFailed, err)
			}
		}
		return fmt.Errorf("download object: %w", err)
	}

	logger.Info("storage download finished",
		zap.Duration("duration", time.Since(start)),
		zap.String("status", "success"),
		zap.Int("status_code", http.StatusOK),
	)
	return nil
}

func (a *Adapter) Upload(ctx context.Context, bucket, key, path, contentType string) error {
	start := time.Now()
	logger := a.logger.With(
		zap.String("bucket", bucket),
		zap.String("key", key),
		zap.String("path", path),
		zap.String("content_type", contentType),
	)
	logger.Info("starting storage upload")

	opts := minio.PutObjectOptions{ContentType: contentType}

	if _, err := a.client.FPutObject(ctx, bucket, key, path, opts); err != nil {
		var resp minio.ErrorResponse
		statusCode := statusCodeFromError(err)
		logger.Error("storage upload failed",
			zap.Duration("duration", time.Since(start)),
			zap.String("status", "error"),
			zap.Int("status_code", statusCode),
			zap.Error(err),
		)
		if errors.As(err, &resp) && resp.StatusCode == 404 {
			return fmt.Errorf("%w: %v", ports.ErrBucketNotFound, err)
		}

		return fmt.Errorf("upload object: %w", err)
	}

	logger.Info("storage upload finished",
		zap.Duration("duration", time.Since(start)),
		zap.String("status", "success"),
		zap.Int("status_code", http.StatusOK),
	)
	return nil
}

func statusCodeFromError(err error) int {
	var resp minio.ErrorResponse
	if errors.As(err, &resp) {
		return resp.StatusCode
	}

	return 0
}
