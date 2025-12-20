package config

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// LoadTestConfig загружает конфигурацию из пути по умолчанию или из CONFIG_PATH,
// позволяя перекрывать только чувствительные поля через переменные окружения.
// Для чувствительных значений уже поддержаны POSTGRES_DSN и MINIO_*.
func LoadTestConfig(t *testing.T) Config {
	t.Helper()

	cfgPath := os.Getenv("CONFIG_PATH")
	if cfgPath == "" {
		cfgPath = DefaultConfigPath
	}

	absPath, err := resolveConfigPath(cfgPath)
	if err != nil {
		t.Fatalf("resolve config path: %v", err)
	}

	cfg, err := Load(absPath)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}

	return cfg
}

func resolveConfigPath(path string) (string, error) {
	if filepath.IsAbs(path) {
		return path, nil
	}

	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		return "", errors.New("resolve caller path")
	}

	root := filepath.Join(filepath.Dir(filename), "..", "..")

	return filepath.Abs(filepath.Join(root, path))
}
