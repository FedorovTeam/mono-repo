//go:build integration
// +build integration

package converter

import (
	"context"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"github.com/mono-repo/docling-transformer-service/internal/config"
	"github.com/mono-repo/docling-transformer-service/internal/domain"
	"github.com/mono-repo/docling-transformer-service/internal/infrastructure/telemetry"
	"github.com/mono-repo/docling-transformer-service/internal/testutil"
	"go.uber.org/zap"
)

func TestDoclingConverterSourceAsyncWritesMarkdownToMinio(t *testing.T) {
	configPath, err := filepath.Abs(filepath.Join("..", "..", "..", "config", "config.yaml"))
	if err != nil {
		t.Fatalf("не удалось вычислить путь до конфига: %v", err)
	}
	t.Setenv("CONFIG_PATH", configPath)

	cfg := testutil.LoadTestConfig(t)
	cfg.Docling.Mode = "source_async"
	cfg.Docling.OCREngine = "easyocr"
	if err := cfg.Validate(); err != nil {
		t.Fatalf("конфигурация недействительна для source_async: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()

	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = http.ProxyFromEnvironment

	minioClient, err := minio.New(cfg.Minio.Endpoint, &minio.Options{
		Creds:     credentials.NewStaticV4(cfg.Minio.AccessKey, cfg.Minio.SecretKey, ""),
		Secure:    cfg.Minio.UseSSL,
		Region:    cfg.Minio.Region,
		Transport: transport,
	})
	if err != nil {
		t.Fatalf("не удалось создать minio client: %v", err)
	}

	if buckets, err := minioClient.ListBuckets(ctx); err != nil {
		t.Fatalf("не удалось получить список бакетов: %v", err)
	} else {
		t.Logf("Доступные бакеты: %v", buckets)
	}

	sourceBucket := cfg.Minio.Bucket
	if sourceBucket == cfg.Docling.Storage.Bucket {
		sourceBucket = "knowledge-original"
	}
	targetBucket := cfg.Docling.Storage.Bucket
	ensureBucket(ctx, t, minioClient, sourceBucket, cfg.Minio.Region)
	if targetBucket != sourceBucket {
		ensureBucket(ctx, t, minioClient, targetBucket, cfg.Docling.Storage.Region)
	}

	prefix := path.Join("tests", "source-async", time.Now().Format("20060102"), uuid.NewString())
	sourceKey := path.Join(prefix, "input.pdf")
	targetKey := path.Join(prefix, "result.md")

	existingSourceKey, err := findExistingSourceObject(ctx, t, minioClient, sourceBucket)
	if err != nil {
		t.Fatalf("не удалось выбрать исходный объект в бакете %s: %v", sourceBucket, err)
	}

	sourceExt := strings.TrimPrefix(strings.ToLower(path.Ext(existingSourceKey)), ".")
	contentType, err := domain.ResolveContentTypeByExtension(sourceExt)
	if err != nil {
		contentType = "application/pdf"
	}

	if _, err := minioClient.CopyObject(ctx,
		minio.CopyDestOptions{Bucket: sourceBucket, Object: sourceKey},
		minio.CopySrcOptions{Bucket: sourceBucket, Object: existingSourceKey},
	); err != nil {
		t.Fatalf("не удалось скопировать исходник %s: %v", existingSourceKey, err)
	}
	t.Logf("Скопирован исходник: %s -> %s/%s", existingSourceKey, sourceBucket, sourceKey)

	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cleanupCancel()
		for _, object := range []struct {
			bucket string
			key    string
		}{
			{bucket: targetBucket, key: targetKey},
			{bucket: sourceBucket, key: sourceKey},
		} {
			_ = minioClient.RemoveObject(cleanupCtx, object.bucket, object.key, minio.RemoveObjectOptions{})
		}
	})

	doclingClient := &http.Client{Transport: doclingEndpointTransport{base: http.DefaultTransport}}

	converter := NewDoclingConverter(Config{
		Endpoint:         cfg.Docling.Endpoint,
		APIKey:           cfg.Docling.APIKey,
		DefaultLanguage:  cfg.Docling.DefaultLanguage,
		OCREngine:        cfg.Docling.OCREngine,
		OCRLang:          cfg.Docling.OCRLang,
		SkipOCROnTextPDF: cfg.Docling.SkipOCROnTextPDF,
		Mode:             cfg.Docling.Mode,
		Storage: StorageConfig{
			Endpoint:  cfg.Docling.Storage.Endpoint,
			AccessKey: cfg.Docling.Storage.AccessKey,
			SecretKey: cfg.Docling.Storage.SecretKey,
			Bucket:    cfg.Docling.Storage.Bucket,
			Region:    cfg.Docling.Storage.Region,
			UseSSL:    cfg.Docling.Storage.UseSSL,
		},
		Render: RenderConfig{
			PreferJSON: cfg.DoclingRender.PreferJSON != nil && *cfg.DoclingRender.PreferJSON,
		},
	}, zap.NewNop(), WithHTTPClient(doclingClient), WithEngineKind("KFP"))

	convertCtx, corrID := telemetry.ContextWithCorrelationID(ctx, "integration-source-async")
	ids, err := converter.Submit(convertCtx, domain.ConvertRequest{
		FileName:        path.Base(sourceKey),
		ContentType:     contentType,
		SourceBucket:    sourceBucket,
		SourceObjectKey: sourceKey,
		TargetBucket:    targetBucket,
		TargetObjectKey: targetKey,
	})
	if err != nil {
		t.Fatalf("submit завершился ошибкой: %v", err)
	}

	t.Logf("Docling task accepted: task_id=%s correlation_id=%s target=%s/%s", ids.TaskID, corrID, targetBucket, targetKey)

	status := waitForDoclingResult(convertCtx, t, converter, ids.TaskID, cfg)
	if status.Result == nil {
		t.Fatalf("Docling вернул пустой результат со статусом %s", status.Status)
	}

	markdown := waitForMinioObject(convertCtx, t, minioClient, targetBucket, targetKey)
	if len(markdown) == 0 {
		t.Fatalf("Markdown в MinIO пустой")
	}

	localPath := filepath.Join(t.TempDir(), "converted.md")
	if err := os.WriteFile(localPath, markdown, 0o644); err != nil {
		t.Fatalf("не удалось сохранить результат локально: %v", err)
	}

	t.Logf("Markdown сохранён локально: %s", localPath)
}

func ensureBucket(ctx context.Context, t *testing.T, client *minio.Client, bucket, region string) {
	t.Helper()

	exists, err := client.BucketExists(ctx, bucket)
	if err != nil {
		t.Fatalf("не удалось проверить наличие bucket %s: %v", bucket, err)
	}
	if exists {
		return
	}

	if err := client.MakeBucket(ctx, bucket, minio.MakeBucketOptions{Region: region}); err != nil {
		t.Fatalf("не удалось создать bucket %s: %v", bucket, err)
	}
}

func findExistingSourceObject(ctx context.Context, t *testing.T, client *minio.Client, bucket string) (string, error) {
	t.Helper()

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	for object := range client.ListObjects(ctx, bucket, minio.ListObjectsOptions{Recursive: true}) {
		if object.Err != nil {
			return "", object.Err
		}

		ext := strings.ToLower(path.Ext(object.Key))
		if ext == ".pdf" || ext == ".docx" || ext == ".doc" {
			return object.Key, nil
		}
	}

	return "", fmt.Errorf("не найден подходящий объект в бакете %s", bucket)
}

type doclingEndpointTransport struct {
	base http.RoundTripper
}

func (t doclingEndpointTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	base := t.base
	if base == nil {
		base = http.DefaultTransport
	}

	cloned := req.Clone(req.Context())
	cloned.Header.Set("X-Docling-Endpoint", "KFP")
	cloned.Header.Set("X-Docling-Engine", "KFP")

	return base.RoundTrip(cloned)
}

func waitForDoclingResult(ctx context.Context, t *testing.T, conv *DoclingConverter, taskID string, cfg *config.Config) domain.StatusCheckResult {
	t.Helper()

	var status domain.StatusCheckResult
	var err error

	interval := cfg.Docling.StatusInitialInterval
	for attempt := 1; attempt <= cfg.Docling.StatusMaxAttempts; attempt++ {
		status, err = conv.CheckStatus(ctx, taskID)
		if err != nil {
			t.Fatalf("ошибка CheckStatus (попытка %d): %v", attempt, err)
		}

		if status.Result != nil {
			return status
		}

		if attempt == cfg.Docling.StatusMaxAttempts {
			break
		}

		sleep := interval
		if sleep <= 0 {
			sleep = 2 * time.Second
		}

		select {
		case <-ctx.Done():
			t.Fatalf("контекст завершён до получения результата Docling: %v", ctx.Err())
		case <-time.After(sleep):
		}

		next := time.Duration(math.Round(float64(interval) * cfg.Docling.StatusBackoffFactor))
		if next <= 0 {
			next = sleep
		}
		if cfg.Docling.StatusMaxInterval > 0 && next > cfg.Docling.StatusMaxInterval {
			next = cfg.Docling.StatusMaxInterval
		}
		interval = next
	}

	t.Fatalf("Docling не успел вернуть результат за %d попыток, последний статус: %s", cfg.Docling.StatusMaxAttempts, status.Status)
	return domain.StatusCheckResult{}
}

func waitForMinioObject(ctx context.Context, t *testing.T, client *minio.Client, bucket, key string) []byte {
	t.Helper()

	var content []byte
	var err error

	for attempt := 1; attempt <= 5; attempt++ {
		var objInfo minio.ObjectInfo
		objInfo, err = client.StatObject(ctx, bucket, key, minio.StatObjectOptions{})
		if err == nil {
			reader, getErr := client.GetObject(ctx, bucket, key, minio.GetObjectOptions{})
			if getErr != nil {
				t.Fatalf("не удалось открыть объект %s/%s: %v", bucket, key, getErr)
			}
			defer reader.Close()

			content, err = io.ReadAll(reader)
			if err != nil {
				t.Fatalf("не удалось прочитать объект %s/%s: %v", bucket, key, err)
			}

			if objInfo.Size != -1 && objInfo.Size != int64(len(content)) {
				t.Logf("предупреждение: размер объекта %d не совпадает с прочитанным %d", objInfo.Size, len(content))
			}
			return content
		}

		if attempt == 5 {
			break
		}

		select {
		case <-ctx.Done():
			t.Fatalf("контекст завершён до появления Markdown в MinIO: %v", ctx.Err())
		case <-time.After(time.Duration(attempt) * time.Second):
		}
	}

	t.Fatalf("не удалось найти Markdown в бакете %s по ключу %s: %v", bucket, key, err)
	return nil
}
