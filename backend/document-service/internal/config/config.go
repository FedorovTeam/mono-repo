package config

import (
	"fmt"
	"gopkg.in/yaml.v3"
	"os"
	"time"
)

type Config struct {
	Server struct {
		Addr string `yaml:"addr"`
	} `yaml:"server"`
	Postgres struct {
		DSN string `yaml:"dsn"`
	} `yaml:"postgres"`
	Minio struct {
		Endpoint       string `yaml:"endpoint"`
		AccessKey      string `yaml:"access_key"`
		SecretKey      string `yaml:"secret_key"`
		UseSSL         bool   `yaml:"use_ssl"`
		OriginalBucket string `yaml:"original_bucket"`
		MarkdownBucket string `yaml:"markdown_bucket"`
		BucketPrefix   string `yaml:"bucket_prefix"`
	} `yaml:"minio"`
	Services struct {
		StorageService         string     `yaml:"storage_service"`
		TransformerService     string     `yaml:"transformer_service"`
		GithubPublisherService string     `yaml:"github_publisher_service"`
		GRPC                   GRPCClient `yaml:"grpc"`
	} `yaml:"services"`
	Github struct {
		Owner      string `yaml:"owner"`
		Repository string `yaml:"repository"`
		Branch     string `yaml:"branch"`
		BaseBranch string `yaml:"base_branch"`
		BaseURL    string `yaml:"base_url"`
	} `yaml:"github"`
	Observability Observability `yaml:"observability"`
	Reconciler    Reconciler    `yaml:"reconciler"`
	GithubRetryer GithubRetryer `yaml:"github_retryer"`
	JobFollower   JobFollower   `yaml:"job_follower"`
}

type GRPCClient struct {
	DialTimeout       time.Duration `yaml:"dial_timeout"`
	MinConnectTimeout time.Duration `yaml:"min_connect_timeout"`
	Backoff           Backoff       `yaml:"backoff"`
	Keepalive         Keepalive     `yaml:"keepalive"`
}

type Backoff struct {
	BaseDelay  time.Duration `yaml:"base_delay"`
	Multiplier float64       `yaml:"multiplier"`
	Jitter     float64       `yaml:"jitter"`
	MaxDelay   time.Duration `yaml:"max_delay"`
}

type Keepalive struct {
	Time                time.Duration `yaml:"time"`
	Timeout             time.Duration `yaml:"timeout"`
	PermitWithoutStream bool          `yaml:"permit_without_stream"`
}

type Observability struct {
	Logs struct {
		Enabled bool `yaml:"enabled"`
	} `yaml:"logs"`
	Traces struct {
		Enabled bool `yaml:"enabled"`
	} `yaml:"traces"`
	Metrics struct {
		Enabled bool `yaml:"enabled"`
	} `yaml:"metrics"`
}

type Reconciler struct {
	Interval       time.Duration `yaml:"interval"`
	MinUploadedAge time.Duration `yaml:"min_uploaded_age"`
	BatchSize      int           `yaml:"batch_size"`
}

type GithubRetryer struct {
	Interval       time.Duration `yaml:"interval"`
	BatchSize      int           `yaml:"batch_size"`
	MaxAttempts    int           `yaml:"max_attempts"`
	InitialBackoff time.Duration `yaml:"initial_backoff"`
	MaxBackoff     time.Duration `yaml:"max_backoff"`
}

type JobFollower struct {
	Interval        time.Duration `yaml:"interval"`
	BatchSize       int           `yaml:"batch_size"`
	RescheduleAfter time.Duration `yaml:"reschedule_after"`
}

func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("validate config: %w", err)
	}
	return &cfg, nil
}

// Validate проверяет обязательные поля и интервальные параметры конфигурации.
func (c *Config) Validate() error {
	if c.Server.Addr == "" {
		return fmt.Errorf("server.addr is required")
	}
	if c.Postgres.DSN == "" {
		return fmt.Errorf("postgres.dsn is required")
	}
	if c.Minio.Endpoint == "" || c.Minio.AccessKey == "" || c.Minio.SecretKey == "" || c.Minio.OriginalBucket == "" || c.Minio.MarkdownBucket == "" {
		return fmt.Errorf("minio configuration is incomplete")
	}
	if c.Services.StorageService == "" || c.Services.TransformerService == "" || c.Services.GithubPublisherService == "" {
		return fmt.Errorf("services endpoints must be provided")
	}

	if c.Reconciler.Interval <= 0 || c.Reconciler.BatchSize <= 0 {
		return fmt.Errorf("reconciler.interval and batch_size must be positive")
	}
	if c.GithubRetryer.Interval <= 0 || c.GithubRetryer.BatchSize <= 0 {
		return fmt.Errorf("github_retryer.interval and batch_size must be positive")
	}
	if c.GithubRetryer.MaxAttempts <= 0 {
		return fmt.Errorf("github_retryer.max_attempts must be positive")
	}
	if c.JobFollower.Interval <= 0 || c.JobFollower.BatchSize <= 0 {
		return fmt.Errorf("job_follower.interval and batch_size must be positive")
	}
	return nil
}
