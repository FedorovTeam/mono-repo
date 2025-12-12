package config

import (
	"os"
	"path/filepath"
	"testing"
)

const baseConfig = `service:
  grpc_port: ":9090"
  document_service: "document-service"

processing:
  dsn: "postgres://user:pass@localhost:5432/processing"
  stage: "store_original"
  poll_interval_seconds: 3
  max_attempts: 5
  backoff_seconds: 7

minio:
  endpoint: "localhost:9000"
  access_key: "minioadmin"
  secret_key: "minioadmin"
  use_ssl: false
  buckets:
    knowledge_docx: "docx"
    knowledge_md: "md"

events:
  upload_bucket: "docx"
  webhook_arn: "arn:minio:sqs::1:storage-webhook"
`

func TestLoadConfigDefaultsObservability(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yaml")

	configContent := []byte(baseConfig)

	writeTestConfig(t, configPath, configContent)

	t.Setenv("CONFIG_PATH", configPath)

	cfg := LoadConfig()
	if cfg == nil {
		t.Fatal("конфигурация должна загружаться")
	}

	if !cfg.Observability.EnableMetrics || !cfg.Observability.EnableTraces || !cfg.Observability.EnableLogs {
		t.Fatalf("по умолчанию все каналы observability должны быть включены: %+v", cfg.Observability)
	}
}

func TestLoadConfigOverridesObservability(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yaml")

	configContent := []byte(baseConfig + `
observability:
  enable_metrics: false
  enable_traces: false
  enable_logs: false
`)

	writeTestConfig(t, configPath, configContent)

	t.Setenv("CONFIG_PATH", configPath)

	cfg := LoadConfig()
	if cfg == nil {
		t.Fatal("конфигурация должна загружаться")
	}

	if cfg.Observability.EnableMetrics || cfg.Observability.EnableTraces || cfg.Observability.EnableLogs {
		t.Fatalf("настройки observability должны переопределяться значениями из файла: %+v", cfg.Observability)
	}
}

func TestLoadConfigSetsProcessingDefaults(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yaml")

	configContent := []byte(`service:
  grpc_port: ":9090"
processing:
  dsn: "postgres://user:pass@localhost:5432/processing"
minio:
  endpoint: "localhost:9000"
  access_key: "minioadmin"
  secret_key: "minioadmin"
  use_ssl: false
  buckets:
    knowledge_docx: "docx"
    knowledge_md: "md"
events:
  upload_bucket: "docx"
  webhook_arn: "arn:minio:sqs::1:storage-webhook"
`)

	writeTestConfig(t, configPath, configContent)

	t.Setenv("CONFIG_PATH", configPath)

	cfg := LoadConfig()
	if cfg == nil {
		t.Fatal("конфигурация должна загружаться")
	}

	if cfg.Processing.Stage == "" || cfg.Processing.PollIntervalSeconds != 5 || cfg.Processing.MaxAttempts != 3 || cfg.Processing.BackoffSeconds != 5 {
		t.Fatalf("значения Processing должны дополняться дефолтами: %+v", cfg.Processing)
	}
}

func writeTestConfig(t *testing.T, path string, data []byte) {
	t.Helper()

	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("не удалось записать config.yaml: %v", err)
	}
}
