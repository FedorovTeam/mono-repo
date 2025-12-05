package storage

import (
	"bytes"
	"context"
	"fmt"
	"io"

	"github.com/minio/minio-go/v7"
)

// ObjectStorage описывает операции чтения/записи объектов в MinIO.
type ObjectStorage interface {
	Download(ctx context.Context, bucket, objectKey string) ([]byte, error)
	Upload(ctx context.Context, bucket, objectKey, contentType string, content []byte) error
}

// MinioStorage реализует ObjectStorage поверх minio.Client.
type MinioStorage struct {
	client *minio.Client
}

// NewMinioStorage создаёт адаптер MinIO.
func NewMinioStorage(client *minio.Client) *MinioStorage {
	return &MinioStorage{client: client}
}

// Download читает объект из указанного бакета.
func (m *MinioStorage) Download(ctx context.Context, bucket, objectKey string) ([]byte, error) {
	obj, err := m.client.GetObject(ctx, bucket, objectKey, minio.GetObjectOptions{})
	if err != nil {
		return nil, fmt.Errorf("get object: %w", err)
	}
	defer obj.Close()

	data, err := io.ReadAll(obj)
	if err != nil {
		return nil, fmt.Errorf("read object: %w", err)
	}

	return data, nil
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
