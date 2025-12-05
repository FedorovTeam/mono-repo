package config

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

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
	Temporal struct {
		Address   string `yaml:"address"`
		Namespace string `yaml:"namespace"`
		TaskQueue string `yaml:"taskQueue"`
	} `yaml:"temporal"`
	Logging struct {
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
	if cfg.Temporal.TaskQueue == "" {
		return Config{}, fmt.Errorf("temporal.taskQueue is required")
	}

	return cfg, nil
}
