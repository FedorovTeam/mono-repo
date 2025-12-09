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
	Temporal struct {
		Address   string `yaml:"address"`
		Namespace string `yaml:"namespace"`
		TaskQueue string `yaml:"task_queue"`
	} `yaml:"temporal"`
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

func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	return &cfg, nil
}
