package config

import (
	"log"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

// Config описывает параметры сервиса хранения.
type Config struct {
	Service       ServiceConfig       `yaml:"service"`
	Minio         MinioConfig         `yaml:"minio"`
	Events        EventsConfig        `yaml:"events"`
	Observability ObservabilityConfig `yaml:"observability"`
	Processing    ProcessingConfig    `yaml:"processing"`
	Uploads       UploadConfig        `yaml:"uploads"`
}

// ServiceConfig содержит сетевые настройки сервисного слоя.
type ServiceConfig struct {
	GRPCPort        string `yaml:"grpc_port"`
	DocumentService string `yaml:"document_service"`
}

// EventsConfig описывает настройки подписок на события MinIO.
type EventsConfig struct {
	UploadBucket string `yaml:"upload_bucket"`
	WebhookArn   string `yaml:"webhook_arn"`
}

// MinioConfig хранит доступы и информацию о бакетах.
type MinioConfig struct {
	Endpoint  string       `yaml:"endpoint"`
	AccessKey string       `yaml:"access_key"`
	SecretKey string       `yaml:"secret_key"`
	UseSSL    bool         `yaml:"use_ssl"`
	Buckets   BucketConfig `yaml:"buckets"`
}

// BucketConfig описывает приватные бакеты для исходников и Markdown.
type BucketConfig struct {
	KnowledgeDocx string `yaml:"knowledge_docx"`
	KnowledgeMD   string `yaml:"knowledge_md"`
}

// UploadConfig описывает лимиты на загрузку и подписанные ссылки.
type UploadConfig struct {
	MaxSizeBytes       int64    `yaml:"max_size_bytes"`
	AllowedExtensions  []string `yaml:"allowed_extensions"`
	SignedURLTTLSecond int64    `yaml:"signed_url_ttl_seconds"`
}

// ObservabilityConfig управляет экспортом метрик, трейсов и логов.
type ObservabilityConfig struct {
	EnableMetrics bool `yaml:"enable_metrics"`
	EnableTraces  bool `yaml:"enable_traces"`
	EnableLogs    bool `yaml:"enable_logs"`
}

// ProcessingConfig описывает параметры воркера обработки processing_jobs.
type ProcessingConfig struct {
	DSN                 string `yaml:"dsn"`
	Stage               string `yaml:"stage"`
	PollIntervalSeconds int    `yaml:"poll_interval_seconds"`
	MaxAttempts         int    `yaml:"max_attempts"`
	BackoffSeconds      int    `yaml:"backoff_seconds"`
}

// PollInterval возвращает интервал опроса processing_jobs с дефолтом 5 секунд.
func (p ProcessingConfig) PollInterval() time.Duration {
	if p.PollIntervalSeconds <= 0 {
		return 5 * time.Second
	}

	return time.Duration(p.PollIntervalSeconds) * time.Second
}

// Backoff возвращает базовый backoff между повторными попытками выполнения задачи.
func (p ProcessingConfig) Backoff() time.Duration {
	if p.BackoffSeconds <= 0 {
		return 5 * time.Second
	}

	return time.Duration(p.BackoffSeconds) * time.Second
}

// LoadConfig загружает конфигурацию из config.yaml или возвращает nil при ошибке.
func LoadConfig() *Config {
	configPath := os.Getenv("CONFIG_PATH")
	if configPath == "" {
		configPath = "config/config.yaml"
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		log.Printf("cannot read config.yaml: %v", err)
		return nil
	}

	cfg := Config{
		Observability: ObservabilityConfig{
			EnableMetrics: true,
			EnableTraces:  true,
			EnableLogs:    true,
		},
		Processing: ProcessingConfig{
			Stage:               "store_original",
			PollIntervalSeconds: 5,
			MaxAttempts:         3,
			BackoffSeconds:      5,
		},
		Uploads: UploadConfig{
			MaxSizeBytes:       10 * 1024 * 1024,
			AllowedExtensions:  []string{"doc", "docx", "pdf"},
			SignedURLTTLSecond: 900,
		},
	}

	if err := yaml.Unmarshal(data, &cfg); err != nil {
		log.Printf("cannot parse config.yaml: %v", err)
		return nil
	}

	if cfg.Processing.Stage == "" {
		cfg.Processing.Stage = "store_original"
	}
	if cfg.Processing.PollIntervalSeconds <= 0 {
		cfg.Processing.PollIntervalSeconds = 5
	}
	if cfg.Processing.MaxAttempts <= 0 {
		cfg.Processing.MaxAttempts = 3
	}
	if cfg.Processing.BackoffSeconds <= 0 {
		cfg.Processing.BackoffSeconds = 5
	}

	if cfg.Uploads.MaxSizeBytes <= 0 {
		cfg.Uploads.MaxSizeBytes = 10 * 1024 * 1024
	}
	if len(cfg.Uploads.AllowedExtensions) == 0 {
		cfg.Uploads.AllowedExtensions = []string{"doc", "docx", "pdf"}
	}
	if cfg.Uploads.SignedURLTTLSecond <= 0 {
		cfg.Uploads.SignedURLTTLSecond = 900
	}

	return &cfg
}
