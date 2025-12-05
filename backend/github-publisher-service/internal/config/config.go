package config

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

const defaultConfigPath = "config/config.yaml"

// Config описывает настройки сервиса и GitHub клиента.
type Config struct {
	Service ServiceConfig `yaml:"service"`
	GitHub  GitHubConfig  `yaml:"github"`
}

// ServiceConfig содержит основные параметры запуска.
type ServiceConfig struct {
	Name     string `yaml:"name"`
	GRPCPort int    `yaml:"grpc_port"`
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

	return &cfg, nil
}
