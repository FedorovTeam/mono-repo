// backend/docling-transformer-service/internal/config/config.go
package config

import (
	"fmt"
	"os"
	"time"

	"github.com/mono-repo/docling-transformer-service/internal/backoff"
	"gopkg.in/yaml.v3"
)

const (
	defaultStatusInitialInterval = 2 * time.Second
	defaultStatusMaxInterval     = 10 * time.Second
	defaultStatusMaxAttempts     = 10
	defaultStatusBackoffFactor   = 2
)

// Config описывает параметры для gRPC сервера, подключения к Docling и инфраструктурных сервисов.
type Config struct {
	Service       ServiceConfig       `yaml:"service"`
	Scheduler     SchedulerConfig     `yaml:"scheduler"`
	Docling       DoclingConfig       `yaml:"docling"`
	DoclingRender DoclingRenderConfig `yaml:"docling_render"`
	Minio         MinioConfig         `yaml:"minio"`
	Postgres      PostgresConfig      `yaml:"postgres"`
	Observability ObservabilityConfig `yaml:"observability"`
}

// ServiceConfig описывает базовые настройки сервиса.
type ServiceConfig struct {
	Name              string        `yaml:"name"`
	GRPCPort          int           `yaml:"grpc_port"`
	MaxWorkers        int           `yaml:"max_workers"`
	QueueLimit        int           `yaml:"queue_limit"`
	ProcessingTimeout time.Duration `yaml:"processing_timeout"`
	IOTimeout         time.Duration `yaml:"io_timeout"`
}

// SchedulerConfig описывает интервалы и параметры повторных попыток для cron-воркеров.
type SchedulerConfig struct {
	PollInterval         time.Duration `yaml:"poll_interval"`
	MaintenanceInterval  time.Duration `yaml:"maintenance_interval"`
	BatchSize            int           `yaml:"batch_size"`
	RetryBackoff         time.Duration `yaml:"retry_backoff"`
	MaxProcessingRetries int           `yaml:"max_processing_retries"`
	CleanupRetention     time.Duration `yaml:"cleanup_retention"`
	FailedRetryDelay     time.Duration `yaml:"failed_retry_delay"`
	MaxFailedRetries     int           `yaml:"max_failed_retries"`
	DoclingRequeueDelay  time.Duration `yaml:"docling_requeue_delay"`
	DoclingMaxRestarts   int           `yaml:"docling_max_restarts"`
}

// DoclingConfig описывает URL Docling-конвертера и публичный endpoint для проксирования.
type DoclingConfig struct {
	Endpoint              string               `yaml:"endpoint"`
	APIKey                string               `yaml:"api_key"`
	DefaultLanguage       string               `yaml:"default_language"`
	OCREngine             string               `yaml:"ocr_engine"`
	OCRLang               []string             `yaml:"ocr_lang"`
	SkipOCROnTextPDF      *bool                `yaml:"skip_ocr_on_text_pdf"`
	Mode                  string               `yaml:"mode"`
	ImageExportMode       string               `yaml:"image_export_mode"`
	IncludeImages         *bool                `yaml:"include_images"`
	Storage               DoclingStorageConfig `yaml:"storage"`
	StatusInitialInterval time.Duration        `yaml:"status_initial_interval"`
	StatusBackoffFactor   float64              `yaml:"status_backoff_factor"`
	StatusMaxInterval     time.Duration        `yaml:"status_max_interval"`
	StatusMaxAttempts     int                  `yaml:"status_max_attempts"`
	MaxJobLifetime        time.Duration        `yaml:"max_job_lifetime"`
}

// DoclingRenderConfig управляет выбором источника данных и стратегией merge для Docling.
type DoclingRenderConfig struct {
	PreferJSON *bool `yaml:"prefer_json"`
}

type DoclingStorageConfig struct {
	Endpoint  string `yaml:"endpoint"`
	AccessKey string `yaml:"access_key"`
	SecretKey string `yaml:"secret_key"`
	Bucket    string `yaml:"bucket"`
	Region    string `yaml:"region"`
	UseSSL    bool   `yaml:"use_ssl"`
}

// MinioConfig описывает параметры доступа к MinIO.
type MinioConfig struct {
	Endpoint  string `yaml:"endpoint"`
	AccessKey string `yaml:"access_key"`
	SecretKey string `yaml:"secret_key"`
	Bucket    string `yaml:"bucket"`
	Region    string `yaml:"region"`
	UseSSL    bool   `yaml:"use_ssl"`
}

// PostgresConfig описывает параметры подключения к базе данных.
type PostgresConfig struct {
	DSN            string `yaml:"dsn"`
	MaxConnections int32  `yaml:"max_connections"`
}

// ObservabilityConfig описывает флаги включения подсистем наблюдаемости.
type ObservabilityConfig struct {
	TracingEnabled bool `yaml:"tracingEnabled"`
	MetricsEnabled bool `yaml:"metricsEnabled"`
	LoggingEnabled bool `yaml:"loggingEnabled"`
}

// Load загружает конфигурацию из файла YAML.
func Load(path string) (*Config, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}

	var cfg Config
	if err := yaml.Unmarshal(content, &cfg); err != nil {
		return nil, fmt.Errorf("unmarshal config: %w", err)
	}

	cfg.applyDefaults()

	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("validate config: %w", err)
	}

	return &cfg, nil
}

// MustLoad читает путь из переменной окружения CONFIG_PATH или использует значение по умолчанию.
func MustLoad() *Config {
	path := os.Getenv("CONFIG_PATH")
	if path == "" {
		path = "config/config.yaml"
	}

	return MustLoadFromPath(path)
}

// MustLoadFromPath загружает конфигурацию из указанного пути и паникует при ошибке.
func MustLoadFromPath(path string) *Config {
	cfg, err := Load(path)
	if err != nil {
		panic(err)
	}

	return cfg
}

// Validate проверяет, что обязательные параметры заданы и имеют допустимые значения.
func (c *Config) Validate() error {
	if c.Service.Name == "" {
		return fmt.Errorf("service.name is required")
	}
	if c.Service.GRPCPort <= 0 {
		return fmt.Errorf("service.grpc_port must be positive")
	}
	if c.Service.QueueLimit < 0 {
		return fmt.Errorf("service.queue_limit cannot be negative")
	}
	if c.Service.ProcessingTimeout <= 0 {
		return fmt.Errorf("service.processing_timeout must be positive")
	}
	if c.Service.IOTimeout <= 0 {
		return fmt.Errorf("service.io_timeout must be positive")
	}

	if c.Scheduler.BatchSize <= 0 {
		return fmt.Errorf("scheduler.batch_size must be positive")
	}
	if c.Scheduler.PollInterval <= 0 {
		return fmt.Errorf("scheduler.poll_interval must be positive")
	}
	if c.Scheduler.MaintenanceInterval <= 0 {
		return fmt.Errorf("scheduler.maintenance_interval must be positive")
	}
	if c.Scheduler.MaxProcessingRetries < 0 {
		return fmt.Errorf("scheduler.max_processing_retries cannot be negative")
	}
	if c.Scheduler.CleanupRetention < 0 {
		return fmt.Errorf("scheduler.cleanup_retention cannot be negative")
	}
	if c.Scheduler.FailedRetryDelay < 0 {
		return fmt.Errorf("scheduler.failed_retry_delay cannot be negative")
	}
	if c.Scheduler.MaxFailedRetries < 0 {
		return fmt.Errorf("scheduler.max_failed_retries cannot be negative")
	}
	if c.Scheduler.DoclingRequeueDelay < 0 {
		return fmt.Errorf("scheduler.docling_requeue_delay cannot be negative")
	}
	if c.Scheduler.DoclingMaxRestarts < 0 {
		return fmt.Errorf("scheduler.docling_max_restarts cannot be negative")
	}
	if c.Postgres.DSN == "" {
		return fmt.Errorf("postgres.dsn is required")
	}
	if c.Minio.Endpoint == "" || c.Minio.AccessKey == "" || c.Minio.SecretKey == "" || c.Minio.Bucket == "" {
		return fmt.Errorf("minio configuration is incomplete")
	}
	if c.Docling.Endpoint == "" {
		return fmt.Errorf("docling.endpoint is required")
	}
	mode := c.Docling.Mode
	if mode == "" {
		mode = "upload_file"
	}
	if mode != "upload_file" && mode != "source_async" {
		return fmt.Errorf("docling.mode must be either upload_file or source_async")
	}
	if mode == "source_async" {
		storage := c.Docling.Storage
		if storage.Endpoint == "" || storage.AccessKey == "" || storage.SecretKey == "" || storage.Bucket == "" {
			return fmt.Errorf("docling.storage configuration is required for source_async mode")
		}
	}
	if c.Docling.OCREngine == "" {
		return fmt.Errorf("docling.ocr_engine is required")
	}

	if err := c.StatusBackoffPolicy().Validate(); err != nil {
		return fmt.Errorf("docling status backoff: %w", err)
	}

	return nil
}

func (c *Config) applyDefaults() {
	policy := c.StatusBackoffPolicy()
	c.Docling.StatusInitialInterval = policy.InitialInterval
	c.Docling.StatusBackoffFactor = policy.Factor
	c.Docling.StatusMaxInterval = policy.MaxInterval
	c.Docling.StatusMaxAttempts = policy.MaxAttempts

	if c.Docling.Mode == "" {
		c.Docling.Mode = "upload_file"
	}

	if c.Docling.OCREngine == "" {
		c.Docling.OCREngine = "easyocr"
	}

	if c.Docling.SkipOCROnTextPDF == nil {
		defaultSkip := true
		c.Docling.SkipOCROnTextPDF = &defaultSkip
	}

	if c.Docling.OCRLang == nil && c.Docling.DefaultLanguage != "" {
		c.Docling.OCRLang = []string{c.Docling.DefaultLanguage}
	}

	if c.Docling.Storage.Endpoint == "" && c.Docling.Storage.AccessKey == "" && c.Docling.Storage.SecretKey == "" && c.Docling.Storage.Bucket == "" {
		c.Docling.Storage = DoclingStorageConfig{
			Endpoint:  c.Minio.Endpoint,
			AccessKey: c.Minio.AccessKey,
			SecretKey: c.Minio.SecretKey,
			Bucket:    c.Minio.Bucket,
			Region:    c.Minio.Region,
			UseSSL:    c.Minio.UseSSL,
		}
	} else {
		if c.Docling.Storage.Endpoint == "" {
			c.Docling.Storage.Endpoint = c.Minio.Endpoint
		}
		if c.Docling.Storage.AccessKey == "" {
			c.Docling.Storage.AccessKey = c.Minio.AccessKey
		}
		if c.Docling.Storage.SecretKey == "" {
			c.Docling.Storage.SecretKey = c.Minio.SecretKey
		}
		if c.Docling.Storage.Bucket == "" {
			c.Docling.Storage.Bucket = c.Minio.Bucket
		}
		if c.Docling.Storage.Region == "" {
			c.Docling.Storage.Region = c.Minio.Region
		}
	}

	if c.DoclingRender.PreferJSON == nil {
		defaultPreferJSON := true
		c.DoclingRender.PreferJSON = &defaultPreferJSON
	}
}

// StatusBackoffPolicy возвращает backoff-политику для опроса статуса Docling
// с учётом дефолтных значений.
func (c *Config) StatusBackoffPolicy() backoff.Policy {
	policy := backoff.Policy{
		InitialInterval: c.Docling.StatusInitialInterval,
		MaxInterval:     c.Docling.StatusMaxInterval,
		MaxAttempts:     c.Docling.StatusMaxAttempts,
		Factor:          c.Docling.StatusBackoffFactor,
	}

	return policy.Normalize(backoff.Policy{
		InitialInterval: defaultStatusInitialInterval,
		MaxInterval:     defaultStatusMaxInterval,
		MaxAttempts:     defaultStatusMaxAttempts,
		Factor:          defaultStatusBackoffFactor,
	})
}
