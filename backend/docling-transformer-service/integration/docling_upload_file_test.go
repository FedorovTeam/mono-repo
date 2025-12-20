//go:build integration
// +build integration

package integration

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mono-repo/docling-transformer-service/internal/backoff"
	"github.com/mono-repo/docling-transformer-service/internal/domain"
	"github.com/mono-repo/docling-transformer-service/internal/infrastructure/converter"
	"github.com/mono-repo/docling-transformer-service/internal/testutil"
	"go.uber.org/zap"
)

const (
	inputPDFPath       = "backend/docling-transformer-service/testdata/input.pdf"
	outputRelativePath = "docling/upload_file_result.md"
	defaultJobTimeout  = 5 * time.Minute
)

func TestDoclingUploadFile(t *testing.T) {
	t.Parallel()

	cfg := testutil.LoadTestConfig(t)

	content, err := os.ReadFile(inputPDFPath)
	if err != nil {
		t.Fatalf("не удалось прочитать входной файл %s: %v", inputPDFPath, err)
	}

	preferJSON := true
	if cfg.DoclingRender.PreferJSON != nil {
		preferJSON = *cfg.DoclingRender.PreferJSON
	}

	conv := converter.NewDoclingConverter(converter.Config{
		Endpoint:         cfg.Docling.Endpoint,
		APIKey:           cfg.Docling.APIKey,
		DefaultLanguage:  cfg.Docling.DefaultLanguage,
		OCREngine:        cfg.Docling.OCREngine,
		OCRLang:          cfg.Docling.OCRLang,
		SkipOCROnTextPDF: cfg.Docling.SkipOCROnTextPDF,
		Mode:             cfg.Docling.Mode,
		Storage: converter.StorageConfig{
			Endpoint:  cfg.Docling.Storage.Endpoint,
			AccessKey: cfg.Docling.Storage.AccessKey,
			SecretKey: cfg.Docling.Storage.SecretKey,
			Bucket:    cfg.Docling.Storage.Bucket,
			Region:    cfg.Docling.Storage.Region,
			UseSSL:    cfg.Docling.Storage.UseSSL,
		},
		Render: converter.RenderConfig{PreferJSON: preferJSON},
	}, zap.NewNop())

	timeout := cfg.Docling.MaxJobLifetime
	if timeout <= 0 {
		timeout = defaultJobTimeout
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	request := domain.ConvertRequest{
		FileName: filepath.Base(inputPDFPath),
		Content:  content,
	}

	ids, err := conv.Submit(ctx, request)
	if err != nil {
		t.Fatalf("submit failed: %v", err)
	}

	policy := cfg.StatusBackoffPolicy()
	strategy := backoff.NewStrategy(policy)

	for attempt := 1; attempt <= policy.MaxAttempts; attempt++ {
		status, err := conv.CheckStatus(ctx, ids.TaskID)
		if err != nil {
			t.Fatalf("check status attempt %d failed: %v", attempt, err)
		}

		if status.Result != nil {
			savedPath := testutil.SaveResult(t, *status.Result, fmt.Sprintf("%s/%s", filepath.Dir(outputRelativePath), ids.TaskID+".md"))
			t.Logf("конвертация завершена, статус=%s, файл=%s", status.Status, savedPath)
			return
		}

		time.Sleep(strategy.NextInterval())
	}

	t.Fatalf("результат конвертации не получен за %d попыток", policy.MaxAttempts)
}
