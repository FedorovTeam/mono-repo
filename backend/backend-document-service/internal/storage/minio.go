package storage

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/minio/minio-go/v7"
)

type KeyPair struct {
	OriginalObjectKey string
	MarkdownObjectKey string
}

type KeyManager interface {
	GenerateKeys(ctx context.Context) (KeyPair, error)
}

type MinioKeyManager struct {
	client         *minio.Client
	prefix         string
	originalBucket string
	markdownBucket string
}

func NewMinioKeyManager(client *minio.Client, prefix, originalBucket, markdownBucket string) *MinioKeyManager {
	return &MinioKeyManager{
		client:         client,
		prefix:         prefix,
		originalBucket: originalBucket,
		markdownBucket: markdownBucket,
	}
}

func (m *MinioKeyManager) GenerateKeys(ctx context.Context) (KeyPair, error) {
	if err := m.ensureBucket(ctx, m.originalBucket); err != nil {
		return KeyPair{}, err
	}
	if err := m.ensureBucket(ctx, m.markdownBucket); err != nil {
		return KeyPair{}, err
	}
	id := uuid.NewString()
	base := fmt.Sprintf("%s/%s", m.prefix, id)
	return KeyPair{
		OriginalObjectKey: base + "/original",
		MarkdownObjectKey: base + "/markdown",
	}, nil
}

func (m *MinioKeyManager) ensureBucket(ctx context.Context, bucket string) error {
	exists, err := m.client.BucketExists(ctx, bucket)
	if err != nil {
		return err
	}
	if !exists {
		return fmt.Errorf("bucket %s does not exist", bucket)
	}
	return nil
}
