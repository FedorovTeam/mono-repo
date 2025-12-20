package usecase

import (
	"context"
	"errors"
	"io"
	"net/url"
	"os"
)

type testFileCache struct {
	storage StorageClient
}

func newTestFileCache(storage StorageClient) FileCache {
	return &testFileCache{storage: storage}
}

func (c *testFileCache) Fetch(ctx context.Context, source SourceLocation) (CachedFile, error) {
	if c.storage == nil {
		return CachedFile{}, errors.New("storage client is not configured")
	}

	switch {
	case source.SourceURL != "":
		parsed, err := url.Parse(source.SourceURL)
		if err != nil {
			return CachedFile{}, err
		}
		if parsed.Scheme == "file" || parsed.Scheme == "" {
			localPath := parsed.Path
			if localPath == "" {
				localPath = source.SourceURL
			}
			return c.fetchFromPath(localPath)
		}
		return CachedFile{}, errors.New("unsupported source url in tests")
	case source.LocalPath != "":
		return c.fetchFromPath(source.LocalPath)
	default:
		reader, err := c.storage.Download(ctx, source.Original.Bucket, source.Original.ObjectKey)
		if err != nil {
			return CachedFile{}, err
		}
		defer reader.Close()
		return writeTemp(reader)
	}
}

func (c *testFileCache) fetchFromPath(path string) (CachedFile, error) {
	reader, err := os.Open(path)
	if err != nil {
		return CachedFile{}, err
	}
	defer reader.Close()
	return writeTemp(reader)
}

func writeTemp(reader io.Reader) (CachedFile, error) {
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
