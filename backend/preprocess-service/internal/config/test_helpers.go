package config

import (
	"errors"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func LoadTestConfig(t *testing.T) Config {
	t.Helper()

	cfgPath := "config/testdata/config.yaml"

	absPath, err := resolveConfigPath(cfgPath)
	if err != nil {
		t.Fatalf("resolve config path: %v", err)
	}

	cfg, err := Load(absPath)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}

	// ВАЖНО: тесты запускаются из разных cwd (IDE/CLI),
	// поэтому делаем абсолютные пути относительно корня preprocess-service.
	root, err := resolveProjectRoot()
	if err != nil {
		t.Fatalf("resolve project root: %v", err)
	}

	cfg.Preprocess.WorkingDir = absFromRoot(root, cfg.Preprocess.WorkingDir)
	cfg.Preprocess.OutputDir = absFromRoot(root, cfg.Preprocess.OutputDir)
	cfg.Preprocess.LocalSourceDir = absFromRoot(root, cfg.Preprocess.LocalSourceDir)
	cfg.Preprocess.LocalTargetDir = absFromRoot(root, cfg.Preprocess.LocalTargetDir)

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

func absFromRoot(root, p string) string {
	p = strings.TrimSpace(p)
	if p == "" || filepath.IsAbs(p) {
		return p
	}
	return filepath.Clean(filepath.Join(root, p))
}

func resolveProjectRoot() (string, error) {
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		return "", errors.New("resolve caller path")
	}
	// internal/config -> preprocess-service
	root := filepath.Join(filepath.Dir(filename), "..", "..")
	return filepath.Abs(root)
}
