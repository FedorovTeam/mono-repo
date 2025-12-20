package filecache

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mono-repo/table-extractor-service/internal/usecase"
)

// TempCache загружает исходный файл во временную директорию и предоставляет путь с коллбеком очистки.
type TempCache struct {
	storage usecase.StorageClient
}

func NewTempCache(storage usecase.StorageClient) *TempCache {
	return &TempCache{storage: storage}
}

func (c *TempCache) Fetch(ctx context.Context, source usecase.SourceLocation) (usecase.CachedFile, error) {
	if strings.TrimSpace(source.SourceURL) != "" {
		return c.fetchFromURL(ctx, source.SourceURL)
	}

	if strings.TrimSpace(source.LocalPath) != "" {
		return c.copyLocalFile(source.LocalPath)
	}

	if c.storage == nil {
		return usecase.CachedFile{}, errors.New("storage client is not configured")
	}

	if strings.TrimSpace(source.Original.Bucket) == "" || strings.TrimSpace(source.Original.ObjectKey) == "" {
		return usecase.CachedFile{}, errors.New("original location is empty")
	}

	reader, err := c.storage.Download(ctx, source.Original.Bucket, source.Original.ObjectKey)
	if err != nil {
		return usecase.CachedFile{}, err
	}
	defer reader.Close()

	return writeTempFile(reader)
}

func (c *TempCache) fetchFromURL(ctx context.Context, value string) (usecase.CachedFile, error) {
	parsed, err := url.Parse(value)
	if err != nil {
		return usecase.CachedFile{}, fmt.Errorf("parse source_url: %w", err)
	}

	switch parsed.Scheme {
	case "", "file":
		path := parsed.Path
		if path == "" {
			path = value
		}
		if !filepath.IsAbs(path) {
			if strings.HasPrefix(value, "file://") {
				path = filepath.Clean("/" + strings.TrimPrefix(value, "file://"))
			} else {
				path, err = filepath.Abs(path)
				if err != nil {
					return usecase.CachedFile{}, fmt.Errorf("resolve path: %w", err)
				}
			}
		}
		return c.copyLocalFile(path)
	case "http", "https":
		return downloadToTemp(ctx, parsed.String())
	default:
		return usecase.CachedFile{}, fmt.Errorf("unsupported source scheme: %s", parsed.Scheme)
	}
}

func (c *TempCache) copyLocalFile(path string) (usecase.CachedFile, error) {
	if strings.TrimSpace(path) == "" {
		return usecase.CachedFile{}, errors.New("local path is empty")
	}

	cleanPath := filepath.Clean(path)
	if !filepath.IsAbs(cleanPath) {
		absPath, err := filepath.Abs(cleanPath)
		if err != nil {
			return usecase.CachedFile{}, fmt.Errorf("resolve local path: %w", err)
		}
		cleanPath = absPath
	}

	reader, err := os.Open(cleanPath)
	if err != nil {
		return usecase.CachedFile{}, err
	}
	defer reader.Close()

	return writeTempFile(reader)
}

func downloadToTemp(ctx context.Context, url string) (usecase.CachedFile, error) {
	client := &http.Client{Timeout: 15 * time.Second}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return usecase.CachedFile{}, fmt.Errorf("create request: %w", err)
	}

	resp, err := client.Do(req)
	if err != nil {
		return usecase.CachedFile{}, fmt.Errorf("download url: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return usecase.CachedFile{}, fmt.Errorf("download url: unexpected status %s", resp.Status)
	}

	return writeTempFile(resp.Body)
}

func writeTempFile(reader io.Reader) (usecase.CachedFile, error) {
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
