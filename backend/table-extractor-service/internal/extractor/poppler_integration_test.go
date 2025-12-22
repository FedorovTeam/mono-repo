//go:build integration && poppler
// +build integration,poppler

package extractor

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/mono-repo/table-extractor-service/internal/config"
)

func TestPopplerIntegration(t *testing.T) {
	binaries := []string{"pdfinfo", "pdftotext", "pdftoppm"}
	for _, bin := range binaries {
		if _, err := exec.LookPath(bin); err != nil {
			t.Skipf("%s недоступен в PATH: %v", bin, err)
		}
	}

	cfg := config.MustLoad("config/config.yaml")
	tempDir := cfg.Extractor.TempDir
	if tempDir == "" {
		tempDir = t.TempDir()
	}

	adapter := NewAdapter(Config{
		PdfInfoTimeout:      cfg.Extractor.PdfInfoTimeout,
		PdftotextTimeout:    cfg.Extractor.PdftotextTimeout,
		PdftoppmTimeout:     cfg.Extractor.PdftoppmTimeout,
		MaxParallelCommands: cfg.Extractor.MaxParallel,
		Pages:               cfg.Extractor.Pages,
		DetectVotingTables:  cfg.Extractor.DetectVotingTables,
		TempDir:             tempDir,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	_, currentFile, _, _ := runtime.Caller(0)
	projectRoot := filepath.Clean(filepath.Join(filepath.Dir(currentFile), "..", ".."))
	pdfPath := prepareTestPDF(t, projectRoot)
	result, err := adapter.Extract(ctx, pdfPath)
	require.NoError(t, err)

	tmpOutput := t.TempDir()
	markdownPath := filepath.Join(tmpOutput, "tables.md")
	manifestPath := filepath.Join(tmpOutput, "manifest.json")

	require.NoError(t, os.WriteFile(markdownPath, result.Markdown, 0o644))
	require.NoError(t, os.WriteFile(manifestPath, result.Manifest, 0o644))
	t.Logf("Markdown сохранён в %s", markdownPath)
	t.Logf("Manifest сохранён в %s", manifestPath)

	require.NotEmpty(t, result.Markdown, "markdown не должен быть пустым")
	require.NotEmpty(t, result.Manifest, "manifest не должен быть пустым")
	require.NotEmpty(t, result.RawJSON, "сырой JSON не должен быть пустым")
	require.NotEmpty(t, result.NormalizedJSON, "нормализованный JSON не должен быть пустым")
	require.NotEmpty(t, result.DebugFiles, "debug файлы не должны быть пустыми")

	var manifest Manifest
	require.NoError(t, json.Unmarshal(result.Manifest, &manifest))
	require.Greater(t, manifest.TableCount, 0, "должна быть извлечена хотя бы одна таблица")
	require.Greater(t, manifest.PDFInfo.Pages, 0, "pdfinfo должен сообщать количество страниц")

	infoOutput, err := exec.CommandContext(ctx, "pdfinfo", pdfPath).CombinedOutput()
	require.NoError(t, err, "pdfinfo завершился с ошибкой: %s", string(infoOutput))

	info := parsePDFInfo(string(infoOutput))
	require.Equal(t, info.Pages, manifest.PDFInfo.Pages, "количество страниц должно совпадать с pdfinfo")

	_, hasPdfInfo := result.DebugFiles["pdfinfo.txt"]
	require.True(t, hasPdfInfo, "должен быть сохранён вывод pdfinfo")

	_, hasLayout := result.DebugFiles["pdftotext.layout.txt"]
	require.True(t, hasLayout, "должен быть сохранён вывод pdftotext")
}

func prepareTestPDF(t *testing.T, projectRoot string) string {
	t.Helper()

	pdfPath := filepath.Join(projectRoot, "testdata", "input.pdf")
	if _, err := os.Stat(pdfPath); err == nil {
		return pdfPath
	}

	base64Path := filepath.Join(projectRoot, "fixtures", "regular", "document.base64")
	data, err := os.ReadFile(base64Path)
	require.NoError(t, err, "не удалось прочитать base64 фикстуру")

	decoded := make([]byte, base64.StdEncoding.DecodedLen(len(data)))
	n, err := base64.StdEncoding.Decode(decoded, bytes.TrimSpace(data))
	require.NoError(t, err, "не удалось декодировать base64 PDF")

	require.NoError(t, os.MkdirAll(filepath.Dir(pdfPath), 0o755))
	require.NoError(t, os.WriteFile(pdfPath, decoded[:n], 0o644))

	t.Cleanup(func() {
		_ = os.Remove(pdfPath)
	})

	return pdfPath
}
