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
preprocess:
  libreoffice_path: /usr/bin/libreoffice
  working_dir: /tmp/work
  output_dir: /tmp/out
  minio:
    endpoint: minio:9000
    access_key: demo
    secret_key: demo-secret
    bucket: preprocess
    ssl: false
    region: us-east-1
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

    if cfg.Preprocess.Minio.Bucket != "preprocess" {
        t.Fatalf("unexpected bucket: %s", cfg.Preprocess.Minio.Bucket)
    }

    if cfg.Preprocess.LibreOfficePath != "/usr/bin/libreoffice" {
        t.Fatalf("unexpected libreoffice path: %s", cfg.Preprocess.LibreOfficePath)
    }
}
