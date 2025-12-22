package config

import (
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

const defaultConfigPath = "config/config.yaml"

type Config struct {
	Service       ServiceConfig       `yaml:"service"`
	Minio         MinioConfig         `yaml:"minio"`
	Database      DatabaseConfig      `yaml:"database"`
	Storage       StorageConfig       `yaml:"storage"`
	Extractor     ExtractorConfig     `yaml:"extractor"`
	Observability ObservabilityConfig `yaml:"observability"`
}

type ServiceConfig struct {
	Name                   string        `yaml:"name"`
	Port                   int           `yaml:"port"`
	QueueLimit             int           `yaml:"queue_limit"`
	BatchSize              int           `yaml:"batch_size"`
	MaxWorkers             int           `yaml:"max_workers"`
	ProcessingTimeout      time.Duration `yaml:"processing_timeout"`
	CleanupRetention       time.Duration `yaml:"cleanup_retention"`
	Retry                  RetryConfig   `yaml:"retry"`
	DefaultArtifactsPrefix string        `yaml:"default_artifacts_prefix"`
}

type RetryConfig struct {
	MaxAttempts     int           `yaml:"max_attempts"`
	BackoffInterval time.Duration `yaml:"backoff_interval"`
	MaxBackoff      time.Duration `yaml:"max_backoff"`
}

type MinioConfig struct {
	Endpoint               string `yaml:"endpoint"`
	AccessKey              string `yaml:"access_key"`
	SecretKey              string `yaml:"secret_key"`
	UseSSL                 bool   `yaml:"use_ssl"`
	ArtifactsBucket        string `yaml:"artifacts_bucket"`
	DefaultArtifactsPrefix string `yaml:"default_artifacts_prefix"`
}

type DatabaseConfig struct {
	Driver            string        `yaml:"driver"`
	DSN               string        `yaml:"dsn"`
	MaxConnections    int           `yaml:"max_connections"`
	ConnectionTimeout time.Duration `yaml:"connection_timeout"`
}

type StorageConfig struct {
	DialTimeout      time.Duration `yaml:"dial_timeout"`
	ReadWriteTimeout time.Duration `yaml:"read_write_timeout"`
	OperationTimeout time.Duration `yaml:"operation_timeout"`
}

type ExtractorConfig struct {
	Mode               string        `yaml:"mode"`
	PdfInfoPath        string        `yaml:"pdfinfo_path"`
	PdftotextPath      string        `yaml:"pdftotext_path"`
	PdftoppmPath       string        `yaml:"pdftoppm_path"`
	DetectVotingTables bool          `yaml:"detect_voting_tables"`
	Pages              string        `yaml:"pages"`
	PdfInfoTimeout     time.Duration `yaml:"pdfinfo_timeout"`
	PdftotextTimeout   time.Duration `yaml:"pdftotext_timeout"`
	PdftoppmTimeout    time.Duration `yaml:"pdftoppm_timeout"`
	MaxParallel        int           `yaml:"max_parallel_commands"`
	TempDir            string        `yaml:"temp_dir"`
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
			Port:                   9090,
			DefaultArtifactsPrefix: "tables",
			MaxWorkers:             1,
			BatchSize:              1,
			QueueLimit:             100,
			ProcessingTimeout:      5 * time.Minute,
			CleanupRetention:       7 * 24 * time.Hour,
			Retry: RetryConfig{
				MaxAttempts:     3,
				BackoffInterval: 5 * time.Second,
				MaxBackoff:      time.Minute,
			},
		},
		Minio: MinioConfig{
			Endpoint:               "http://localhost:9000",
			AccessKey:              "minioadmin",
			SecretKey:              "minioadmin",
			ArtifactsBucket:        "table-extractor-artifacts",
			DefaultArtifactsPrefix: "tables",
		},
		Database: DatabaseConfig{
			Driver:            "sqlite",
			DSN:               "file:table-extractor.db?cache=shared&_pragma=foreign_keys(1)",
			MaxConnections:    5,
			ConnectionTimeout: 5 * time.Second,
		},
		Storage: StorageConfig{
			DialTimeout:      5 * time.Second,
			ReadWriteTimeout: 30 * time.Second,
			OperationTimeout: time.Minute,
		},
		Extractor: ExtractorConfig{
			Mode:               "layout",
			PdfInfoPath:        "pdfinfo",
			PdftotextPath:      "pdftotext",
			PdftoppmPath:       "pdftoppm",
			DetectVotingTables: true,
			Pages:              "all",
			PdfInfoTimeout:     10 * time.Second,
			PdftotextTimeout:   20 * time.Second,
			PdftoppmTimeout:    30 * time.Second,
			MaxParallel:        2,
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
