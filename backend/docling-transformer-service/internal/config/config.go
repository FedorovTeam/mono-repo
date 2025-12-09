package config

import (
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

// Config описывает параметры для gRPC сервера, подключения к Docling и инфраструктурных сервисов.
type Config struct {
	Service       ServiceConfig       `yaml:"service"`
	Docling       DoclingConfig       `yaml:"docling"`
	Minio         MinioConfig         `yaml:"minio"`
	Postgres      PostgresConfig      `yaml:"postgres"`
	Temporal      TemporalConfig      `yaml:"temporal"`
	Observability ObservabilityConfig `yaml:"observability"`
}

// ServiceConfig описывает базовые настройки сервиса.
type ServiceConfig struct {
	Name                 string        `yaml:"name"`
	GRPCPort             int           `yaml:"grpc_port"`
	MaxWorkers           int           `yaml:"max_workers"`
	PollInterval         time.Duration `yaml:"poll_interval"`
	ProcessingTimeout    time.Duration `yaml:"processing_timeout"`
	IOTimeout            time.Duration `yaml:"io_timeout"`
	MaxProcessingRetries int           `yaml:"max_processing_retries"`
	CleanupInterval      time.Duration `yaml:"cleanup_interval"`
	RetentionPeriod      time.Duration `yaml:"retention_period"`
}

// DoclingConfig описывает URL Docling-конвертера и публичный endpoint для проксирования.
type DoclingConfig struct {
	Endpoint              string        `yaml:"endpoint"`
	APIKey                string        `yaml:"api_key"`
	StatusPollInterval    time.Duration `yaml:"status_poll_interval"`
	StatusPollTimeout     time.Duration `yaml:"status_poll_timeout"`
	StatusMaxAttempts     int           `yaml:"status_max_attempts"`
	StatusRecheckInterval time.Duration `yaml:"status_recheck_interval"`
	MaxJobLifetime        time.Duration `yaml:"max_job_lifetime"`
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

// TemporalConfig описывает подключение к Temporal.
type TemporalConfig struct {
	Address   string `yaml:"address"`
	Namespace string `yaml:"namespace"`
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
