package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoad(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	content := []byte(`
service:
  name: preprocess-service
  port: 8080
  grpc_port: 9090
  region: ru-msk-1
postgres:
  dsn: postgres://postgres:password@localhost:5432/preprocess?sslmode=disable
minio:
  endpoint: minio:9000
  access_key: demo
  secret_key: demo-secret
  ssl: false
  region: ru-msk-1
preprocess:
  libreoffice_path: /usr/bin/libreoffice
  working_dir: /tmp/work
  output_dir: /tmp/out
  local_source_dir: /tmp/source
  local_target_dir: /tmp/target
  storage_mode: filesystem
  source_bucket: preprocess-source
  target_bucket: preprocess-target
  target_key_prefix: converted
logging:
  level: debug
`)

	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatalf("failed to write config: %v", err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}

	if cfg.Service.Name != "preprocess-service" {
		t.Fatalf("unexpected service name: %s", cfg.Service.Name)
	}

	if cfg.Minio.Endpoint != "minio:9000" {
		t.Fatalf("unexpected minio endpoint: %s", cfg.Minio.Endpoint)
	}

	if cfg.Preprocess.LibreOfficePath != "/usr/bin/libreoffice" {
		t.Fatalf("unexpected libreoffice path: %s", cfg.Preprocess.LibreOfficePath)
	}

	if cfg.Preprocess.StorageModeOrDefault() != StorageModeFilesystem {
		t.Fatalf("unexpected storage mode: %s", cfg.Preprocess.StorageMode)
	}

	if cfg.Preprocess.LocalSourceDir != "/tmp/source" {
		t.Fatalf("unexpected local source dir: %s", cfg.Preprocess.LocalSourceDir)
	}

	if cfg.Preprocess.LocalTargetDir != "/tmp/target" {
		t.Fatalf("unexpected local target dir: %s", cfg.Preprocess.LocalTargetDir)
	}

	if cfg.Preprocess.TargetBucket != "preprocess-target" {
		t.Fatalf("unexpected target bucket: %s", cfg.Preprocess.TargetBucket)
	}

	if cfg.Preprocess.TargetKeyPrefix != "converted" {
		t.Fatalf("unexpected target key prefix: %s", cfg.Preprocess.TargetKeyPrefix)
	}

	if cfg.Preprocess.OutputDir != "/tmp/out" {
		t.Fatalf("unexpected output dir: %s", cfg.Preprocess.OutputDir)
	}
}
