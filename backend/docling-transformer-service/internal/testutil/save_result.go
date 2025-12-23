package testutil

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/mono-repo/docling-transformer-service/internal/domain"
)

const (
	docOutputDirPath     = "DOC_OUTPUT_DIR"
	docOutputDirFlagName = "doc_output_dir"
	docOutputDirDefault  = "./result/"
)

var (
	docOutputDir = flag.String(docOutputDirFlagName, "", "директория для сохранения результатов конвертации (.md)")

	defaultOutputDirOnce sync.Once
	defaultOutputDir     string
	defaultOutputDirErr  error
)

// SaveResult сохраняет результат конвертации в локальный .md файл.
//
// Директория может быть переопределена переменной окружения DOC_OUTPUT_DIR или
// флагом -doc_output_dir. По умолчанию используется временная директория.
// Возвращает путь к созданному файлу.
func SaveResult(t *testing.T, result domain.ConvertResult, relativePath string) string {
	t.Helper()

	if strings.TrimSpace(relativePath) == "" {
		t.Fatalf("relativePath должен быть непустым")
	}

	dir := resolveDocOutputDir(t)
	filename := ensureMarkdownPath(relativePath)
	fullPath := filepath.Join(dir, filename)

	if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
		t.Fatalf("не удалось создать директорию %s: %v", fullPath, err)
	}

	fmt.Println(result.MediaType)
	fmt.Println(result.Charset)
	if err := os.WriteFile(fullPath, result.Content, 0o644); err != nil {
		t.Fatalf("не удалось сохранить результат в %s: %v", fullPath, err)
	}

	return fullPath
}

func resolveDocOutputDir(t *testing.T) string {
	t.Helper()

	if dir := strings.TrimSpace(*docOutputDir); dir != "" {
		ensureDirExists(t, dir)
		return dir
	}

	if dir := strings.TrimSpace(os.Getenv(docOutputDirPath)); dir != "" {
		ensureDirExists(t, dir)
		return dir
	}

	if dir := strings.TrimSpace(docOutputDirDefault); dir != "" {
		ensureDirExists(t, dir)
		return dir
	}

	defaultOutputDirOnce.Do(func() {
		defaultOutputDir, defaultOutputDirErr = os.MkdirTemp("", "doc-output-")
	})

	if defaultOutputDirErr != nil {
		t.Fatalf("не удалось создать временную директорию для сохранения результата: %v", defaultOutputDirErr)
	}

	return defaultOutputDir
}

func ensureMarkdownPath(path string) string {
	clean := filepath.Clean(path)
	clean = strings.TrimLeft(clean, string(filepath.Separator))
	if filepath.Ext(clean) != ".md" {
		clean += ".md"
	}
	return clean
}

func ensureDirExists(t *testing.T, dir string) {
	t.Helper()

	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("не удалось создать директорию %s: %v", dir, err)
	}
}
