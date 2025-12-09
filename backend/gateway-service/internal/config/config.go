package config

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// ObservabilityConfig описывает параметры телеметрии.
type ObservabilityConfig struct {
	Enabled      *bool  `yaml:"enabled"`
	OTLPEndpoint string `yaml:"otlpEndpoint"`
	ServiceName  string `yaml:"serviceName"`
}

// EnabledOrDefault возвращает true, если флаг не задан, и значение флага, если он задан явно.
func (o ObservabilityConfig) EnabledOrDefault() bool {
	if o.Enabled == nil {
		return true
	}

	return *o.Enabled
}

// Config описывает конфигурацию сервиса.
type Config struct {
	Server struct {
		Port int `yaml:"port"`
	} `yaml:"server"`
	Security struct {
		APIKey string `yaml:"apiKey"`
	} `yaml:"security"`
	DocumentService struct {
		Address string `yaml:"address"`
	} `yaml:"documentService"`
	StorageService struct {
		Address string `yaml:"address"`
	} `yaml:"storageService"`
	Uploads struct {
		MaxSizeBytes       int64    `yaml:"maxSizeBytes"`
		AllowedExtensions  []string `yaml:"allowedExtensions"`
		SignedURLTTLSecond int64    `yaml:"signedUrlTtlSeconds"`
	} `yaml:"uploads"`
	Observability ObservabilityConfig `yaml:"observability"`
	Logging       struct {
		Level string `yaml:"level"`
	} `yaml:"logging"`
}

// Load читает конфигурацию из файла.
func Load(path string) (Config, error) {
	file, err := os.Open(path)
	if err != nil {
		return Config{}, fmt.Errorf("failed to open config: %w", err)
	}
	defer file.Close()

	var cfg Config
	decoder := yaml.NewDecoder(file)
	if err := decoder.Decode(&cfg); err != nil {
		return Config{}, fmt.Errorf("failed to parse config: %w", err)
	}

	if cfg.Security.APIKey == "" {
		return Config{}, fmt.Errorf("security.apiKey is required")
	}
	if cfg.DocumentService.Address == "" {
		return Config{}, fmt.Errorf("documentService.address is required")
	}
	if cfg.StorageService.Address == "" {
		return Config{}, fmt.Errorf("storageService.address is required")
	}
	if cfg.Uploads.MaxSizeBytes <= 0 {
		return Config{}, fmt.Errorf("uploads.maxSizeBytes must be positive")
	}
	if len(cfg.Uploads.AllowedExtensions) == 0 {
		return Config{}, fmt.Errorf("uploads.allowedExtensions is required")
	}
	if cfg.Uploads.SignedURLTTLSecond <= 0 {
		cfg.Uploads.SignedURLTTLSecond = 900
	}
	if cfg.Observability.ServiceName == "" {
		cfg.Observability.ServiceName = "backend-gateway-service"
	}
	if cfg.Observability.EnabledOrDefault() && cfg.Observability.OTLPEndpoint == "" {
		return Config{}, fmt.Errorf("observability.otlpEndpoint is required when observability.enabled is true")
	}

	return cfg, nil
}
