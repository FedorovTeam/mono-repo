package config

import (
	"os"

	"gopkg.in/yaml.v3"
)

const defaultConfigPath = "config/config.yaml"

type Config struct {
	Service       ServiceConfig       `yaml:"service"`
	Storage       StorageConfig       `yaml:"storage"`
	Observability ObservabilityConfig `yaml:"observability"`
}

type ServiceConfig struct {
	Name                   string `yaml:"name"`
	GRPCPort               int    `yaml:"grpc_port"`
	QueueLimit             int    `yaml:"queue_limit"`
	DefaultArtifactsPrefix string `yaml:"default_artifacts_prefix"`
}

type StorageConfig struct {
	ArtifactsBucket string `yaml:"artifacts_bucket"`
}

type ObservabilityConfig struct {
	LoggingEnabled bool          `yaml:"logging_enabled"`
	Tracing        TracingConfig `yaml:"tracing"`
}

type TracingConfig struct {
	Enabled bool `yaml:"enabled"`
}

func defaultConfig() Config {
	return Config{
		Service: ServiceConfig{
			Name:                   "table-extractor-service",
			GRPCPort:               9090,
			DefaultArtifactsPrefix: "tables",
		},
		Observability: ObservabilityConfig{
			LoggingEnabled: true,
			Tracing: TracingConfig{
				Enabled: false,
			},
		},
	}
}

func MustLoad(path string) *Config {
	cfg, err := Load(path)
	if err != nil {
		panic(err)
	}
	return cfg
}

func Load(path string) (*Config, error) {
	if path == "" {
		if envPath := os.Getenv("CONFIG_PATH"); envPath != "" {
			path = envPath
		} else {
			path = defaultConfigPath
		}
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	cfg := defaultConfig()
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}

	return &cfg, nil
}
