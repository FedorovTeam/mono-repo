package config

import (
	"testing"
	"time"
)

func TestValidateSuccess(t *testing.T) {
	cfg := Config{
		Service: ServiceConfig{
			Name:              "docling-transformer",
			GRPCPort:          9090,
			MaxWorkers:        1,
			QueueLimit:        10,
			ProcessingTimeout: time.Minute,
			IOTimeout:         time.Second,
		},
		Scheduler: SchedulerConfig{
			PollInterval:         time.Second,
			MaintenanceInterval:  time.Minute,
			BatchSize:            5,
			RetryBackoff:         time.Second,
			MaxProcessingRetries: 1,
			CleanupRetention:     time.Hour,
			FailedRetryDelay:     time.Minute,
			MaxFailedRetries:     3,
		},
		Docling: DoclingConfig{
			Endpoint:  "http://docling",
			OCREngine: "easyocr",
		},
		Minio: MinioConfig{
			Endpoint:  "minio",
			AccessKey: "key",
			SecretKey: "secret",
			Bucket:    "bucket",
		},
		Postgres: PostgresConfig{DSN: "postgres://user:pass@localhost:5432/db"},
	}

	if err := cfg.Validate(); err != nil {
		t.Fatalf("unexpected validation error: %v", err)
	}
}

func TestValidateFailsOnMissingFields(t *testing.T) {
	cfg := Config{}
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected validation error for empty config")
	}
}

func TestValidateFailsWithoutSourceAsyncStorage(t *testing.T) {
	cfg := Config{
		Service: ServiceConfig{
			Name:              "docling-transformer",
			GRPCPort:          9090,
			MaxWorkers:        1,
			QueueLimit:        10,
			ProcessingTimeout: time.Minute,
			IOTimeout:         time.Second,
		},
		Scheduler: SchedulerConfig{
			PollInterval:         time.Second,
			MaintenanceInterval:  time.Minute,
			BatchSize:            5,
			RetryBackoff:         time.Second,
			MaxProcessingRetries: 1,
			CleanupRetention:     time.Hour,
			FailedRetryDelay:     time.Minute,
			MaxFailedRetries:     3,
		},
		Docling: DoclingConfig{Endpoint: "http://docling", Mode: "source_async", OCREngine: "easyocr"},
		Minio: MinioConfig{
			Endpoint:  "minio",
			AccessKey: "key",
			SecretKey: "secret",
			Bucket:    "bucket",
		},
		Postgres: PostgresConfig{DSN: "postgres://user:pass@localhost:5432/db"},
	}

	if err := cfg.Validate(); err == nil {
		t.Fatal("expected validation error for missing docling.storage in source_async mode")
	}
}

func TestStatusBackoffDefaultsApplied(t *testing.T) {
	cfg := Config{}

	policy := cfg.StatusBackoffPolicy()

	if policy.InitialInterval != defaultStatusInitialInterval {
		t.Fatalf("expected default initial interval %v, got %v", defaultStatusInitialInterval, policy.InitialInterval)
	}
	if policy.MaxInterval != defaultStatusMaxInterval {
		t.Fatalf("expected default max interval %v, got %v", defaultStatusMaxInterval, policy.MaxInterval)
	}
	if policy.MaxAttempts != defaultStatusMaxAttempts {
		t.Fatalf("expected default attempts %d, got %d", defaultStatusMaxAttempts, policy.MaxAttempts)
	}
	if policy.Factor != defaultStatusBackoffFactor {
		t.Fatalf("expected default backoff factor %v, got %v", defaultStatusBackoffFactor, policy.Factor)
	}
}

func TestStatusBackoffOverridesConfig(t *testing.T) {
	cfg := Config{Docling: DoclingConfig{
		StatusInitialInterval: time.Second,
		StatusBackoffFactor:   3,
		StatusMaxInterval:     5 * time.Second,
		StatusMaxAttempts:     2,
	}}

	policy := cfg.StatusBackoffPolicy()

	if policy.InitialInterval != time.Second || policy.MaxInterval != 5*time.Second || policy.MaxAttempts != 2 || policy.Factor != 3 {
		t.Fatalf("unexpected policy: %+v", policy)
	}

	cfg.applyDefaults()
	if cfg.Docling.StatusInitialInterval != time.Second || cfg.Docling.StatusMaxInterval != 5*time.Second || cfg.Docling.StatusMaxAttempts != 2 || cfg.Docling.StatusBackoffFactor != 3 {
		t.Fatalf("defaults must not override provided values: %+v", cfg.Docling)
	}
}
