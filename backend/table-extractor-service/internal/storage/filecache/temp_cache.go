package filecache

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"

	"github.com/mono-repo/table-extractor-service/internal/domain"
	"github.com/mono-repo/table-extractor-service/internal/usecase"
)

// TempCache загружает исходный файл во временную директорию и предоставляет путь с коллбеком очистки.
type TempCache struct {
	storage usecase.StorageClient
}

func NewTempCache(storage usecase.StorageClient) *TempCache {
	return &TempCache{storage: storage}
}

func (c *TempCache) Fetch(ctx context.Context, original domain.ArtifactLocation) (usecase.CachedFile, error) {
	if c.storage == nil {
		return usecase.CachedFile{}, errors.New("storage client is not configured")
	}

	if original.Bucket == "" || original.ObjectKey == "" {
		return usecase.CachedFile{}, errors.New("original location is empty")
	}

	reader, err := c.storage.Download(ctx, original.Bucket, original.ObjectKey)
	if err != nil {
		return usecase.CachedFile{}, err
	}
	defer reader.Close()

	file, err := os.CreateTemp("", "table-extractor-*.pdf")
	if err != nil {
		return usecase.CachedFile{}, err
	}

	cleanup := func() {
		_ = os.Remove(file.Name())
	}

	if _, err := io.Copy(file, reader); err != nil {
		_ = file.Close()
		cleanup()
		return usecase.CachedFile{}, err
	}

	if err := file.Close(); err != nil {
		cleanup()
		return usecase.CachedFile{}, err
	}

	return usecase.CachedFile{
		Path:    filepath.Clean(file.Name()),
		Cleanup: cleanup,
	}, nil
}

var _ usecase.FileCache = (*TempCache)(nil)
