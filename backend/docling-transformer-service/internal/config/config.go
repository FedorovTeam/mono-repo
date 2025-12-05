package config

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// Config описывает параметры для gRPC сервера, подключения к Pandoc/Docling и инфраструктурных сервисов.
type Config struct {
	Service  ServiceConfig  `yaml:"service"`
	Pandoc   PandocConfig   `yaml:"pandoc"`
	Docling  DoclingConfig  `yaml:"docling"`
	Minio    MinioConfig    `yaml:"minio"`
	Temporal TemporalConfig `yaml:"temporal"`
}

// ServiceConfig описывает базовые настройки сервиса.
type ServiceConfig struct {
	Name     string `yaml:"name"`
	GRPCPort int    `yaml:"grpc_port"`
}

// PandocConfig содержит URL HTTP-обёртки Pandoc.
type PandocConfig struct {
	Endpoint string `yaml:"endpoint"`
}

// DoclingConfig описывает URL Docling, который используется для дополнительной валидации документов.
type DoclingConfig struct {
	Endpoint string `yaml:"endpoint"`
}

// MinioConfig описывает параметры доступа к MinIO.
type MinioConfig struct {
	Endpoint  string `yaml:"endpoint"`
	AccessKey string `yaml:"access_key"`
	SecretKey string `yaml:"secret_key"`
	Bucket    string `yaml:"bucket"`
	Region    string `yaml:"region"`
}

// TemporalConfig описывает подключение к Temporal.
type TemporalConfig struct {
	Address   string `yaml:"address"`
	Namespace string `yaml:"namespace"`
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

	cfg, err := Load(path)
	if err != nil {
		panic(err)
	}

	return cfg
}
