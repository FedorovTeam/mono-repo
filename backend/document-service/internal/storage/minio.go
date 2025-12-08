package storage

import (
	"context"
	"fmt"
	"path"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/minio/minio-go/v7"
)

type KeyPair struct {
	OriginalObjectKey string
	MarkdownObjectKey string
}

type KeyManager interface {
	GenerateKeys(ctx context.Context, data KeyGenerationData) (KeyPair, error)
}

type MinioKeyManager struct {
	client         *minio.Client
	prefix         string
	originalBucket string
	markdownBucket string
}

type KeyGenerationData struct {
	DocumentID  string
	FileName    string
	Path        string
	PublishPath string
}

func NewMinioKeyManager(client *minio.Client, prefix, originalBucket, markdownBucket string) *MinioKeyManager {
	return &MinioKeyManager{
		client:         client,
		prefix:         prefix,
		originalBucket: originalBucket,
		markdownBucket: markdownBucket,
	}
}

func (m *MinioKeyManager) GenerateKeys(ctx context.Context, data KeyGenerationData) (KeyPair, error) {
	if err := m.ensureBucket(ctx, m.originalBucket); err != nil {
		return KeyPair{}, err
	}
	if err := m.ensureBucket(ctx, m.markdownBucket); err != nil {
		return KeyPair{}, err
	}
	base := m.buildBasePath(data)
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

func (m *MinioKeyManager) buildBasePath(data KeyGenerationData) string {
	parts := make([]string, 0, 6)
	prefix := strings.Trim(m.prefix, "/")
	if prefix != "" {
		parts = append(parts, prefix)
	}

	now := time.Now().UTC()
	parts = append(parts,
		fmt.Sprintf("year=%04d", now.Year()),
		fmt.Sprintf("month=%02d", int(now.Month())),
		fmt.Sprintf("day=%02d", now.Day()),
		buildEntitySpecificPath(data),
	)

	return path.Join(parts...)
}

func buildEntitySpecificPath(data KeyGenerationData) string {
	var segments []string

	if data.DocumentID != "" {
		segments = append(segments, data.DocumentID)
	}

	candidate := firstNonEmpty(
		normalizePath(data.PublishPath),
		normalizePath(data.Path),
		normalizePath(data.FileName),
	)

	if candidate != "" {
		segments = append(segments, candidate)
	}

	if len(segments) == 0 {
		segments = append(segments, uuid.NewString())
	}

	return path.Join(segments...)
}

func normalizePath(value string) string {
	cleaned := strings.Trim(strings.ReplaceAll(value, "\\", "/"), "/")
	if cleaned == "" || cleaned == "." {
		return ""
	}
	return path.Clean(cleaned)
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
