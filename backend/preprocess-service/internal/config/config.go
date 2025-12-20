package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

const DefaultConfigPath = "config/config.yaml"

const (
	StorageModeMinio      = "minio"
	StorageModeFilesystem = "filesystem"
)

type ServiceConfig struct {
	Name     string `yaml:"name"`
	Port     int    `yaml:"port"`
	GRPCPort int    `yaml:"grpc_port"`
	Region   string `yaml:"region"`
}

type MinioConfig struct {
	Endpoint  string `yaml:"endpoint"`
	AccessKey string `yaml:"access_key"`
	SecretKey string `yaml:"secret_key"`
	SSL       bool   `yaml:"ssl"`
	Region    string `yaml:"region"`
}

type PostgresConfig struct {
	DSN string `yaml:"dsn"`
}

type PreprocessConfig struct {
	LibreOfficePath   string        `yaml:"libreoffice_path"`
	WorkingDir        string        `yaml:"working_dir"`
	OutputDir         string        `yaml:"output_dir"`
	LocalSourceDir    string        `yaml:"local_source_dir"`
	LocalTargetDir    string        `yaml:"local_target_dir"`
	StorageMode       string        `yaml:"storage_mode"`
	SourceBucket      string        `yaml:"source_bucket"`
	TargetBucket      string        `yaml:"target_bucket"`
	TargetKeyPrefix   string        `yaml:"target_key_prefix"`
	TargetMediaType   string        `yaml:"target_media_type"`
	WorkerConcurrency int64         `yaml:"worker_concurrency"`
	ConvertTimeout    time.Duration `yaml:"convert_timeout"`
	Backoff           time.Duration `yaml:"backoff"`
	PollInterval      time.Duration `yaml:"poll_interval"`
}

type LoggingConfig struct {
	Level string `yaml:"level"`
}

type Config struct {
	Service    ServiceConfig    `yaml:"service"`
	Postgres   PostgresConfig   `yaml:"postgres"`
	Minio      MinioConfig      `yaml:"minio"`
	Preprocess PreprocessConfig `yaml:"preprocess"`
	Logging    LoggingConfig    `yaml:"logging"`
}

func Load(path string) (Config, error) {
	if path == "" {
		path = DefaultConfigPath
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("read config: %w", err)
	}

	var cfg Config
	if err := yaml.Unmarshal(raw, &cfg); err != nil {
		return Config{}, fmt.Errorf("unmarshal config: %w", err)
	}

	if dsn := os.Getenv("POSTGRES_DSN"); dsn != "" {
		cfg.Postgres.DSN = dsn
	}

	if minioEndpoint := os.Getenv("MINIO_ENDPOINT"); minioEndpoint != "" {
		cfg.Minio.Endpoint = minioEndpoint
	}
	if minioAccessKey := os.Getenv("MINIO_ACCESS_KEY"); minioAccessKey != "" {
		cfg.Minio.AccessKey = minioAccessKey
	}
	if minioSecretKey := os.Getenv("MINIO_SECRET_KEY"); minioSecretKey != "" {
		cfg.Minio.SecretKey = minioSecretKey
	}
	if minioRegion := os.Getenv("MINIO_REGION"); minioRegion != "" {
		cfg.Minio.Region = minioRegion
	}
	if minioSSL := strings.TrimSpace(os.Getenv("MINIO_SSL")); minioSSL != "" {
		parsed, err := strconv.ParseBool(minioSSL)
		if err != nil {
			return Config{}, fmt.Errorf("parse MINIO_SSL: %w", err)
		}
		cfg.Minio.SSL = parsed
	}

	return cfg, nil
}

func (c PreprocessConfig) StorageModeOrDefault() string {
	mode := strings.ToLower(strings.TrimSpace(c.StorageMode))
	if mode == "" {
		return StorageModeMinio
	}
	return mode
}
