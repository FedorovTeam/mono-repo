package testutil

import (
	"os"
	"strconv"
	"testing"

	"github.com/mono-repo/docling-transformer-service/internal/config"
)

const (
	doclingEndpointEnv = "DOCLING_ENDPOINT"

	minioEndpointEnv  = "MINIO_ENDPOINT"
	minioAccessKeyEnv = "MINIO_ACCESS_KEY"
	minioSecretKeyEnv = "MINIO_SECRET_KEY"
	minioBucketEnv    = "MINIO_BUCKET"
	minioRegionEnv    = "MINIO_REGION"
	minioSSLEnv       = "MINIO_SSL"
)

// LoadTestConfig загружает конфигурацию из config/config.yaml или CONFIG_PATH и
// применяет переопределения из переменных окружения для Docling и MinIO.
func LoadTestConfig(t *testing.T) *config.Config {
	t.Helper()

	cfg := config.MustLoad()

	if endpoint := os.Getenv(doclingEndpointEnv); endpoint != "" {
		cfg.Docling.Endpoint = endpoint
	}

	applyMinioOverrides(t, cfg)

	if err := cfg.Validate(); err != nil {
		t.Fatalf("конфигурация некорректна после применения env-override: %v", err)
	}

	return cfg
}

func applyMinioOverrides(t *testing.T, cfg *config.Config) {
	t.Helper()

	minioCfg := cfg.Minio
	doclingStorage := cfg.Docling.Storage

	if endpoint := os.Getenv(minioEndpointEnv); endpoint != "" {
		minioCfg.Endpoint = endpoint
		doclingStorage.Endpoint = endpoint
	}

	if accessKey := os.Getenv(minioAccessKeyEnv); accessKey != "" {
		minioCfg.AccessKey = accessKey
		doclingStorage.AccessKey = accessKey
	}

	if secretKey := os.Getenv(minioSecretKeyEnv); secretKey != "" {
		minioCfg.SecretKey = secretKey
		doclingStorage.SecretKey = secretKey
	}

	if bucket := os.Getenv(minioBucketEnv); bucket != "" {
		minioCfg.Bucket = bucket
		doclingStorage.Bucket = bucket
	}

	if region := os.Getenv(minioRegionEnv); region != "" {
		minioCfg.Region = region
		doclingStorage.Region = region
	}

	if useSSL := os.Getenv(minioSSLEnv); useSSL != "" {
		parsed, err := strconv.ParseBool(useSSL)
		if err != nil {
			t.Fatalf("MINIO_SSL должно быть булевым значением: %v", err)
		}
		minioCfg.UseSSL = parsed
		doclingStorage.UseSSL = parsed
	}

	cfg.Minio = minioCfg
	cfg.Docling.Storage = doclingStorage
}
