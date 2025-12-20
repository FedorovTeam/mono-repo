package fs

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"preprocess-service/internal/usecase/ports"
)

func TestDownloadMissingFile(t *testing.T) {
	sourceDir := t.TempDir()
	targetDir := t.TempDir()

	adapter, err := NewAdapter(sourceDir, targetDir)
	if err != nil {
		t.Fatalf("failed to create adapter: %v", err)
	}

	destPath := filepath.Join(t.TempDir(), "missing.docx")

	err = adapter.Download(context.Background(), "bucket", "incoming/missing.docx", "", destPath)
	if !errors.Is(err, ports.ErrObjectNotFound) {
		t.Fatalf("expected ErrObjectNotFound, got: %v", err)
	}
}

func TestDownloadAndUpload(t *testing.T) {
	sourceDir := t.TempDir()
	targetDir := t.TempDir()
	bucket := "local-bucket"

	adapter, err := NewAdapter(sourceDir, targetDir)
	if err != nil {
		t.Fatalf("failed to create adapter: %v", err)
	}

	sourceKey := "incoming/source.docx"
	sourcePath := filepath.Join(sourceDir, bucket, sourceKey)
	if err := os.MkdirAll(filepath.Dir(sourcePath), 0o755); err != nil {
		t.Fatalf("prepare source dir: %v", err)
	}
	original := []byte("original content")
	if err := os.WriteFile(sourcePath, original, 0o644); err != nil {
		t.Fatalf("write source file: %v", err)
	}

	destPath := filepath.Join(t.TempDir(), "downloaded.docx")
	if err := adapter.Download(context.Background(), bucket, sourceKey, "", destPath); err != nil {
		t.Fatalf("download returned error: %v", err)
	}

	downloaded, err := os.ReadFile(destPath)
	if err != nil {
		t.Fatalf("read downloaded file: %v", err)
	}
	if string(downloaded) != string(original) {
		t.Fatalf("unexpected downloaded content: %s", downloaded)
	}

	targetKey := "converted/result.docx"
	uploadPath := filepath.Join(t.TempDir(), "result.docx")
	converted := []byte("converted content")
	if err := os.WriteFile(uploadPath, converted, 0o644); err != nil {
		t.Fatalf("write upload source: %v", err)
	}

	if err := adapter.Upload(context.Background(), bucket, targetKey, uploadPath, ""); err != nil {
		t.Fatalf("upload returned error: %v", err)
	}

	targetPath := filepath.Join(targetDir, bucket, targetKey)
	saved, err := os.ReadFile(targetPath)
	if err != nil {
		t.Fatalf("read target file: %v", err)
	}
	if string(saved) != string(converted) {
		t.Fatalf("unexpected target content: %s", saved)
	}
}
