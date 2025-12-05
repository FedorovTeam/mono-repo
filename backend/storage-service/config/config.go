package config

import (
	"log"
	"os"

	"gopkg.in/yaml.v3"
)

// Config описывает параметры сервиса хранения.
type Config struct {
	Service ServiceConfig `yaml:"service"`
	Minio   MinioConfig   `yaml:"minio"`
}

// ServiceConfig содержит сетевые настройки сервисного слоя.
type ServiceConfig struct {
	GRPCPort string `yaml:"grpc_port"`
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

// LoadConfig загружает конфигурацию из config.yaml или возвращает nil при ошибке.
func LoadConfig() *Config {
	data, err := os.ReadFile("config/config.yaml")
	if err != nil {
		log.Printf("cannot read config.yaml: %v", err)
		return nil
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		log.Printf("cannot parse config.yaml: %v", err)
		return nil
	}

	return &cfg
}
