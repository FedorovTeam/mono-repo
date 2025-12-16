package config

import (
    "fmt"
    "os"

    "gopkg.in/yaml.v3"
)

const DefaultConfigPath = "config/config.yaml"

type ServiceConfig struct {
    Name    string `yaml:"name"`
    Port    int    `yaml:"port"`
    GRPCPort int   `yaml:"grpc_port"`
    Region  string `yaml:"region"`
}

type MinioConfig struct {
    Endpoint  string `yaml:"endpoint"`
    AccessKey string `yaml:"access_key"`
    SecretKey string `yaml:"secret_key"`
    Bucket    string `yaml:"bucket"`
    SSL       bool   `yaml:"ssl"`
    Region    string `yaml:"region"`
}

type PreprocessConfig struct {
    LibreOfficePath string      `yaml:"libreoffice_path"`
    WorkingDir      string      `yaml:"working_dir"`
    OutputDir       string      `yaml:"output_dir"`
    Minio           MinioConfig `yaml:"minio"`
}

type LoggingConfig struct {
    Level string `yaml:"level"`
}

type Config struct {
    Service    ServiceConfig    `yaml:"service"`
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

    return cfg, nil
}
