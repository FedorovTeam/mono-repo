package storage

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/mono-repo/docling-transformer-service/internal/domain"
)

type ObjectStorage = domain.ObjectStorage

type minioClient interface {
	GetObject(ctx context.Context, bucketName, objectName string, opts minio.GetObjectOptions) (*minio.Object, error)
	PutObject(ctx context.Context, bucketName, objectName string, reader io.Reader, objectSize int64, opts minio.PutObjectOptions) (minio.UploadInfo, error)
}

// MinioStorage реализует ObjectStorage поверх minio.Client.
type MinioStorage struct {
	client minioClient
}

// NewMinioStorage создаёт адаптер MinIO.
func NewMinioStorage(client *minio.Client) *MinioStorage {
	return &MinioStorage{client: client}
}

// Download читает объект из указанного бакета.
func (m *MinioStorage) Download(ctx context.Context, bucket, objectKey string) ([]byte, error) {
	var lastErr error

	for attempt := 1; attempt <= 10; attempt++ {
		obj, err := m.client.GetObject(ctx, bucket, objectKey, minio.GetObjectOptions{})
		if err != nil {
			// Если это "ключ не найден" — подождать и попробовать ещё раз
			if respErr, ok := err.(minio.ErrorResponse); ok && respErr.Code == "NoSuchKey" {
				lastErr = err

				// небольшая экспоненциальная пауза: 0.5s, 1s, 2s, 4s...
				sleep := time.Duration(attempt) * 500 * time.Millisecond

				select {
				case <-ctx.Done():
					return nil, ctx.Err()
				case <-time.After(sleep):
					continue
				}
			}

			// другая ошибка — выходим сразу
			return nil, fmt.Errorf("get object: %w", err)
		}

		defer obj.Close()

		data, err := io.ReadAll(obj)
		if err != nil {
			return nil, fmt.Errorf("read object: %w", err)
		}

		return data, nil
	}

	return nil, fmt.Errorf("read object: %w", lastErr)
}

// Upload сохраняет контент в MinIO.
func (m *MinioStorage) Upload(ctx context.Context, bucket, objectKey, contentType string, content []byte) error {
	reader := bytes.NewReader(content)
	_, err := m.client.PutObject(ctx, bucket, objectKey, reader, int64(len(content)), minio.PutObjectOptions{ContentType: contentType})
	if err != nil {
		return fmt.Errorf("put object: %w", err)
	}

	return nil
}
