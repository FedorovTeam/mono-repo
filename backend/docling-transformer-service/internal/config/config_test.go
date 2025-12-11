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
		},
		Docling: DoclingConfig{Endpoint: "http://docling"},
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
