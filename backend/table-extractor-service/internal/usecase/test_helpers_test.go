package usecase

import (
	"context"
	"errors"
	"io"
	"os"

	"github.com/mono-repo/table-extractor-service/internal/domain"
)

type testFileCache struct {
	storage StorageClient
}

func newTestFileCache(storage StorageClient) FileCache {
	return &testFileCache{storage: storage}
}

func (c *testFileCache) Fetch(ctx context.Context, original domain.ArtifactLocation) (CachedFile, error) {
	if c.storage == nil {
		return CachedFile{}, errors.New("storage client is not configured")
	}

	reader, err := c.storage.Download(ctx, original.Bucket, original.ObjectKey)
	if err != nil {
		return CachedFile{}, err
	}
	defer reader.Close()

	file, err := os.CreateTemp("", "table-extractor-test-*.pdf")
	if err != nil {
		return CachedFile{}, err
	}

	cleanup := func() {
		_ = os.Remove(file.Name())
	}

	if _, err := io.Copy(file, reader); err != nil {
		_ = file.Close()
		cleanup()
		return CachedFile{}, err
	}

	if err := file.Close(); err != nil {
		cleanup()
		return CachedFile{}, err
	}

	return CachedFile{
		Path:    file.Name(),
		Cleanup: cleanup,
	}, nil
}

var _ FileCache = (*testFileCache)(nil)
