package fs

import (
	"context"
	"errors"
	"fmt"
	"io"
	stdfs "io/fs"
	"os"
	"path/filepath"
	"strings"

	"preprocess-service/internal/usecase/ports"
)

// Adapter копирует файлы внутри локальной файловой системы без использования MinIO.
// Источник берётся из каталога sourceDir, результат сохраняется в targetDir.
type Adapter struct {
	sourceDir string
	targetDir string
}

func NewAdapter(sourceDir, targetDir string) (*Adapter, error) {
	if strings.TrimSpace(sourceDir) == "" {
		return nil, fmt.Errorf("source dir is required")
	}
	if strings.TrimSpace(targetDir) == "" {
		return nil, fmt.Errorf("target dir is required")
	}

	if err := os.MkdirAll(sourceDir, 0o755); err != nil {
		return nil, fmt.Errorf("prepare source dir: %w", err)
	}
	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		return nil, fmt.Errorf("prepare target dir: %w", err)
	}

	return &Adapter{
		sourceDir: sourceDir,
		targetDir: targetDir,
	}, nil
}

func (a *Adapter) Download(_ context.Context, bucket, key, _ string, destinationPath string) error {
	sourceKey := a.combineKey(bucket, key)
	sourcePath, err := a.resolvePath(a.sourceDir, sourceKey)
	if err != nil {
		return fmt.Errorf("resolve source path: %w", err)
	}

	if err := os.MkdirAll(filepath.Dir(destinationPath), 0o755); err != nil {
		return fmt.Errorf("prepare destination dir: %w", err)
	}

	source, err := os.Open(sourcePath)
	if err != nil {
		if errors.Is(err, stdfs.ErrNotExist) {
			return fmt.Errorf("%w: %v", ports.ErrObjectNotFound, err)
		}
		return fmt.Errorf("open source file: %w", err)
	}
	defer source.Close()

	dest, err := os.Create(destinationPath)
	if err != nil {
		return fmt.Errorf("create destination file: %w", err)
	}
	defer dest.Close()

	if _, err := io.Copy(dest, source); err != nil {
		return fmt.Errorf("copy file: %w", err)
	}

	return nil
}

func (a *Adapter) Upload(_ context.Context, bucket, key, path, _ string) error {
	targetKey := a.combineKey(bucket, key)
	targetPath, err := a.resolvePath(a.targetDir, targetKey)
	if err != nil {
		return fmt.Errorf("resolve target path: %w", err)
	}

	if err := os.MkdirAll(filepath.Dir(targetPath), 0o755); err != nil {
		return fmt.Errorf("prepare target dir: %w", err)
	}

	source, err := os.Open(path)
	if err != nil {
		if errors.Is(err, stdfs.ErrNotExist) {
			return fmt.Errorf("%w: %v", ports.ErrObjectNotFound, err)
		}
		return fmt.Errorf("open source file: %w", err)
	}
	defer source.Close()

	dest, err := os.Create(targetPath)
	if err != nil {
		return fmt.Errorf("create target file: %w", err)
	}
	defer dest.Close()

	if _, err := io.Copy(dest, source); err != nil {
		return fmt.Errorf("copy file: %w", err)
	}

	return nil
}

func (a *Adapter) resolvePath(baseDir, key string) (string, error) {
	clean := filepath.Clean(key)
	if clean == "" || clean == "." {
		return "", ports.ErrInvalidObjectKey
	}
	if filepath.IsAbs(clean) {
		return "", ports.ErrInvalidObjectKey
	}
	if strings.HasPrefix(clean, ".."+string(filepath.Separator)) || clean == ".." {
		return "", ports.ErrInvalidObjectKey
	}

	return filepath.Join(baseDir, clean), nil
}

func (a *Adapter) combineKey(bucket, key string) string {
	bucket = strings.TrimSpace(bucket)
	if bucket == "" {
		return key
	}
	return filepath.Join(bucket, key)
}
