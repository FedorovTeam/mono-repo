package storage

import (
	"backend-github-publisher-service/pkg/config"
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

// MarkdownLoader умеет загружать Markdown из хранилища.
//
// (генерация моков опциональна, текущие тесты используют простые заглушки)
//
//go:generate mockgen -destination=./mocks_storage_test.go -package=storage . MarkdownLoader
type MarkdownLoader interface {
	LoadMarkdown(ctx context.Context, bucket, key string) ([]byte, error)
}

// MinioMarkdownLoader реализует загрузку Markdown из MinIO.
type MinioMarkdownLoader struct {
	client *minio.Client
}

// NewMinioMarkdownLoader создаёт клиент MinIO на основе конфигурации.
func NewMinioMarkdownLoader(cfg config.MinioConfig) (*MinioMarkdownLoader, error) {
	client, err := minio.New(cfg.Endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(cfg.AccessKey, cfg.SecretKey, ""),
		Secure: cfg.Secure,
	})
	if err != nil {
		return nil, fmt.Errorf("create minio client: %w", err)
	}
	return &MinioMarkdownLoader{client: client}, nil
}

// LoadMarkdown читает содержимое объекта из MinIO.
func (m *MinioMarkdownLoader) LoadMarkdown(ctx context.Context, bucket, key string) ([]byte, error) {
	object, err := m.client.GetObject(ctx, bucket, strings.TrimPrefix(key, "/"), minio.GetObjectOptions{})
	if err != nil {
		return nil, fmt.Errorf("get object: %w", err)
	}
	defer object.Close()

	data, err := io.ReadAll(object)
	if err != nil {
		return nil, fmt.Errorf("read object: %w", err)
	}
	return data, nil
}
