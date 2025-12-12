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
	Storage struct {
		OriginalBucket string `yaml:"original_bucket"`
		MarkdownBucket string `yaml:"markdown_bucket"`
	} `yaml:"storage"`
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
	JobFollower   JobFollower   `yaml:"job_follower"`
	StageWorker   StageWorker   `yaml:"stage_worker"`
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

type JobFollower struct {
	Interval        time.Duration `yaml:"interval"`
	BatchSize       int           `yaml:"batch_size"`
	RescheduleAfter time.Duration `yaml:"reschedule_after"`
}

type StageWorker struct {
	Interval     time.Duration `yaml:"interval"`
	BatchSize    int           `yaml:"batch_size"`
	RetryDelay   time.Duration `yaml:"retry_delay"`
	WaitMarkdown bool          `yaml:"wait_markdown"`
	GracePeriod  time.Duration `yaml:"grace_period"`
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
	if c.Storage.OriginalBucket == "" || c.Storage.MarkdownBucket == "" {
		return fmt.Errorf("storage buckets configuration is incomplete")
	}
	if c.Services.StorageService == "" || c.Services.TransformerService == "" || c.Services.GithubPublisherService == "" {
		return fmt.Errorf("services endpoints must be provided")
	}

	if c.Reconciler.Interval <= 0 || c.Reconciler.BatchSize <= 0 {
		return fmt.Errorf("reconciler.interval and batch_size must be positive")
	}
	if c.JobFollower.Interval <= 0 || c.JobFollower.BatchSize <= 0 {
		return fmt.Errorf("job_follower.interval and batch_size must be positive")
	}
	if c.StageWorker.Interval <= 0 || c.StageWorker.BatchSize <= 0 || c.StageWorker.RetryDelay <= 0 {
		return fmt.Errorf("stage_worker interval, batch_size and retry_delay must be positive")
	}
	if c.StageWorker.GracePeriod < 0 {
		return fmt.Errorf("stage_worker.grace_period cannot be negative")
	}
	return nil
}
