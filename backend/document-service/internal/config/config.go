package config

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Server struct {
		Addr string `yaml:"addr"`
	} `yaml:"server"`
	Postgres struct {
		DSN string `yaml:"dsn"`
	} `yaml:"postgres"`
	Minio struct {
		Endpoint       string `yaml:"endpoint"`
		AccessKey      string `yaml:"access_key"`
		SecretKey      string `yaml:"secret_key"`
		UseSSL         bool   `yaml:"use_ssl"`
		OriginalBucket string `yaml:"original_bucket"`
		MarkdownBucket string `yaml:"markdown_bucket"`
		BucketPrefix   string `yaml:"bucket_prefix"`
	} `yaml:"minio"`
	Temporal struct {
		Address   string `yaml:"address"`
		Namespace string `yaml:"namespace"`
		TaskQueue string `yaml:"task_queue"`
	} `yaml:"temporal"`
	Services struct {
		StorageService         string `yaml:"storage_service"`
		TransformerService     string `yaml:"transformer_service"`
		GithubPublisherService string `yaml:"github_publisher_service"`
	} `yaml:"services"`
	Github struct {
		Repository string `yaml:"repository"`
		Branch     string `yaml:"branch"`
	} `yaml:"github"`
}

func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	return &cfg, nil
}
