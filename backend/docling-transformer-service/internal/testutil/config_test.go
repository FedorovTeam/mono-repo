package testutil

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

const baseTestConfig = `service:
  name: docling-transformer
  grpc_port: 9090
  max_workers: 1
  queue_limit: 1
  processing_timeout: 1m
  io_timeout: 1s

scheduler:
  poll_interval: 1s
  maintenance_interval: 1m
  batch_size: 1
  retry_backoff: 1s
  max_processing_retries: 1
  cleanup_retention: 1m
  failed_retry_delay: 1s
  max_failed_retries: 1

docling:
  endpoint: http://docling-original
  ocr_engine: easyocr

minio:
  endpoint: minio:9000
  access_key: default-key
  secret_key: default-secret
  bucket: default-bucket
  region: us-east-1
  use_ssl: false

postgres:
  dsn: postgres://user:pass@localhost:5432/db

observability:
  tracingEnabled: false
  metricsEnabled: false
  loggingEnabled: false
`

func TestLoadTestConfigAppliesOverrides(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yaml")
	require.NoError(t, os.WriteFile(configPath, []byte(baseTestConfig), 0o644))

	t.Setenv("CONFIG_PATH", configPath)
	t.Setenv("DOCLING_ENDPOINT", "http://docling-test")
	t.Setenv("MINIO_ENDPOINT", "minio:19000")
	t.Setenv("MINIO_ACCESS_KEY", "access")
	t.Setenv("MINIO_SECRET_KEY", "secret")
	t.Setenv("MINIO_BUCKET", "bucket")
	t.Setenv("MINIO_REGION", "ru-west-1")
	t.Setenv("MINIO_SSL", "true")

	cfg := LoadTestConfig(t)

	require.Equal(t, "http://docling-test", cfg.Docling.Endpoint)

	require.Equal(t, "minio:19000", cfg.Minio.Endpoint)
	require.Equal(t, "access", cfg.Minio.AccessKey)
	require.Equal(t, "secret", cfg.Minio.SecretKey)
	require.Equal(t, "bucket", cfg.Minio.Bucket)
	require.Equal(t, "ru-west-1", cfg.Minio.Region)
	require.True(t, cfg.Minio.UseSSL)

	require.Equal(t, cfg.Minio.Endpoint, cfg.Docling.Storage.Endpoint)
	require.Equal(t, cfg.Minio.AccessKey, cfg.Docling.Storage.AccessKey)
	require.Equal(t, cfg.Minio.SecretKey, cfg.Docling.Storage.SecretKey)
	require.Equal(t, cfg.Minio.Bucket, cfg.Docling.Storage.Bucket)
	require.Equal(t, cfg.Minio.Region, cfg.Docling.Storage.Region)
	require.Equal(t, cfg.Minio.UseSSL, cfg.Docling.Storage.UseSSL)
}

func TestLoadTestConfigKeepsDefaultsWithoutOverrides(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yaml")
	require.NoError(t, os.WriteFile(configPath, []byte(baseTestConfig), 0o644))

	t.Setenv("CONFIG_PATH", configPath)

	cfg := LoadTestConfig(t)

	require.Equal(t, "http://docling-original", cfg.Docling.Endpoint)
	require.Equal(t, "minio:9000", cfg.Minio.Endpoint)
	require.Equal(t, cfg.Minio.Endpoint, cfg.Docling.Storage.Endpoint)
	require.Equal(t, cfg.Minio.AccessKey, cfg.Docling.Storage.AccessKey)
	require.Equal(t, cfg.Minio.SecretKey, cfg.Docling.Storage.SecretKey)
	require.Equal(t, cfg.Minio.Bucket, cfg.Docling.Storage.Bucket)
	require.Equal(t, cfg.Minio.Region, cfg.Docling.Storage.Region)
	require.Equal(t, cfg.Minio.UseSSL, cfg.Docling.Storage.UseSSL)
}
