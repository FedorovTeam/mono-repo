package minio

import (
	"context"
	"errors"
	"fmt"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"

	"preprocess-service/internal/config"
	"preprocess-service/internal/usecase/ports"
)

type Adapter struct {
	client *minio.Client
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

func NewAdapter(client *minio.Client) *Adapter {
	return &Adapter{client: client}
}

func (a *Adapter) Download(ctx context.Context, bucket, key, etag, destinationPath string) error {
	opts := minio.GetObjectOptions{}
	if etag != "" {
		if err := opts.SetMatchETag(etag); err != nil {
			return fmt.Errorf("set etag: %w", err)
		}
	}

	if err := a.client.FGetObject(ctx, bucket, key, destinationPath, opts); err != nil {
		var resp minio.ErrorResponse
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

	return nil
}

func (a *Adapter) Upload(ctx context.Context, bucket, key, path, contentType string) error {
	opts := minio.PutObjectOptions{ContentType: contentType}

	if _, err := a.client.FPutObject(ctx, bucket, key, path, opts); err != nil {
		var resp minio.ErrorResponse
		if errors.As(err, &resp) && resp.StatusCode == 404 {
			return fmt.Errorf("%w: %v", ports.ErrBucketNotFound, err)
		}

		return fmt.Errorf("upload object: %w", err)
	}

	return nil
}
