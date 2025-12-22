package config

import (
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

const defaultConfigPath = "config/config.yaml"

// Config описывает настройки сервиса и GitHub клиента.
type Config struct {
	Service       ServiceConfig       `yaml:"service"`
	Logging       LoggingConfig       `yaml:"logging"`
	GitHub        GitHubConfig        `yaml:"github"`
	Minio         MinioConfig         `yaml:"minio"`
	Observability ObservabilityConfig `yaml:"observability"`
	Processing    ProcessingConfig    `yaml:"processing"`
	Retry         RetryConfig         `yaml:"retry"`
}

// ServiceConfig содержит основные параметры запуска.
type ServiceConfig struct {
	Name     string `yaml:"name"`
	GRPCPort int    `yaml:"grpc_port"`
}

// LoggingConfig описывает настройки логирования.
type LoggingConfig struct {
	Level       string `yaml:"level"`
	Encoding    string `yaml:"encoding"`
	Development bool   `yaml:"development"`
}

// GitHubConfig хранит параметры доступа к GitHub API.
type GitHubConfig struct {
	BaseURL           string `yaml:"base_url"`
	Owner             string `yaml:"owner"`
	Repository        string `yaml:"repository"`
	Token             string `yaml:"token"`
	DefaultBranch     string `yaml:"default_branch"`
	AuthorName        string `yaml:"author_name"`
	AuthorEmail       string `yaml:"author_email"`
	CorrelationHeader string `yaml:"correlation_header"`
}

// MinioConfig описывает параметры доступа к MinIO.
type MinioConfig struct {
	Endpoint  string `yaml:"endpoint"`
	AccessKey string `yaml:"access_key"`
	SecretKey string `yaml:"secret_key"`
	Secure    bool   `yaml:"secure"`
}

// ProcessingConfig описывает подключение и параметры воркера, который двигает задачи публикации.
type ProcessingConfig struct {
	DSN          string        `yaml:"dsn"`
	Stage        string        `yaml:"stage"`
	PollInterval time.Duration `yaml:"poll_interval"`
	MaxAttempts  int           `yaml:"max_attempts"`
	Backoff      time.Duration `yaml:"backoff"`
}

// RetryConfig описывает базовую стратегию повтора попыток публикации.
type RetryConfig struct {
	InitialBackoff time.Duration `yaml:"initial_backoff"`
	MaxBackoff     time.Duration `yaml:"max_backoff"`
	MaxAttempts    int           `yaml:"max_attempts"`
}

// ObservabilityConfig описывает настройки телеметрии.
type ObservabilityConfig struct {
	OTLPEndpoint   string `yaml:"otlp_endpoint"`
	ServiceName    string `yaml:"service_name"`
	MetricsEnabled bool   `yaml:"metrics_enabled"`
	TracesEnabled  bool   `yaml:"traces_enabled"`
	LogsEnabled    bool   `yaml:"logs_enabled"`
}

// Load загружает конфигурацию из CONFIG_PATH или файла по умолчанию.
func Load() (*Config, error) {
	path := os.Getenv("CONFIG_PATH")
	if path == "" {
		path = defaultConfigPath
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("unmarshal config: %w", err)
	}

	if cfg.GitHub.CorrelationHeader == "" {
		cfg.GitHub.CorrelationHeader = "X-Correlation-ID"
	}

	if cfg.Logging.Level == "" {
		cfg.Logging.Level = "info"
	}
	if cfg.Logging.Encoding == "" {
		cfg.Logging.Encoding = "json"
	}

	if cfg.Observability.ServiceName == "" {
		cfg.Observability.ServiceName = cfg.Service.Name
	}
	if cfg.Observability.ServiceName == "" {
		cfg.Observability.ServiceName = "github-publisher-service"
	}

	if !cfg.Observability.MetricsEnabled && !cfg.Observability.TracesEnabled && !cfg.Observability.LogsEnabled {
		cfg.Observability.MetricsEnabled = true
		cfg.Observability.TracesEnabled = true
		cfg.Observability.LogsEnabled = true
	}

	if cfg.Processing.Stage == "" {
		cfg.Processing.Stage = "publish_to_github"
	}
	if cfg.Processing.PollInterval <= 0 {
		cfg.Processing.PollInterval = 5 * time.Second
	}
	if cfg.Processing.MaxAttempts <= 0 {
		cfg.Processing.MaxAttempts = 3
	}
	if cfg.Processing.Backoff <= 0 {
		cfg.Processing.Backoff = 5 * time.Second
	}

	if cfg.Retry.InitialBackoff <= 0 {
		cfg.Retry.InitialBackoff = 5 * time.Second
	}
	if cfg.Retry.MaxBackoff <= 0 {
		cfg.Retry.MaxBackoff = 30 * time.Second
	}
	if cfg.Retry.MaxAttempts <= 0 {
		cfg.Retry.MaxAttempts = 3
	}

	return &cfg, nil
}
