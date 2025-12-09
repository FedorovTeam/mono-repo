package config

import (
	"log"
	"os"

	"gopkg.in/yaml.v3"
)

// Config описывает параметры сервиса хранения.
type Config struct {
	Service       ServiceConfig       `yaml:"service"`
	Minio         MinioConfig         `yaml:"minio"`
	Events        EventsConfig        `yaml:"events"`
	Observability ObservabilityConfig `yaml:"observability"`
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

// ObservabilityConfig управляет экспортом метрик, трейсов и логов.
type ObservabilityConfig struct {
	EnableMetrics bool `yaml:"enable_metrics"`
	EnableTraces  bool `yaml:"enable_traces"`
	EnableLogs    bool `yaml:"enable_logs"`
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
	}

	if err := yaml.Unmarshal(data, &cfg); err != nil {
		log.Printf("cannot parse config.yaml: %v", err)
		return nil
	}

	return &cfg
}
