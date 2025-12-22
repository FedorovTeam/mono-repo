package app

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"go.uber.org/zap/zaptest"

	"preprocess-service/internal/config"
	"preprocess-service/internal/storage/memory"
	"preprocess-service/internal/usecase/ports"
)

func TestNewAppWithoutTelemetry(t *testing.T) {
	t.Setenv("CONFIG_PATH", filepath.Join("config", "testdata", "config.yaml"))

	cfg := config.LoadTestConfig(t)
	cfg.Service.Port = 0
	cfg.Service.GRPCPort = 0
	cfg.Preprocess.WorkingDir = t.TempDir()
	cfg.Preprocess.OutputDir = t.TempDir()
	cfg.Preprocess.LocalSourceDir = ""
	cfg.Preprocess.LocalTargetDir = ""
	cfg.Preprocess.ConvertTimeout = time.Second
	cfg.Preprocess.Backoff = time.Second
	cfg.Preprocess.PollInterval = 10 * time.Millisecond
	cfg.Preprocess.WorkerConcurrency = 1

	logger := zaptest.NewLogger(t)

	app, err := New(cfg, Dependencies{
		Logger:           logger,
		Telemetry:        nil,
		DB:               nil,
		Repo:             memory.NewRepository(),
		Downloader:       noopStorage{},
		Uploader:         noopStorage{},
		Converter:        noopConverter{},
		WorkspaceManager: noopWorkspaceManager{},
		Callback:         noopCallback{},
	})
	if err != nil {
		t.Fatalf("failed to create app without telemetry: %v", err)
	}

	if app == nil {
		t.Fatalf("expected app instance, got nil")
	}

	if app.httpServer == nil || app.grpcServer == nil || app.worker == nil {
		t.Fatalf("expected servers and worker to be initialized")
	}
}

type noopStorage struct{}

func (noopStorage) Download(context.Context, string, string, string, string) error { return nil }
func (noopStorage) Upload(context.Context, string, string, string, string) error   { return nil }

type noopConverter struct{}

func (noopConverter) Convert(context.Context, string, string) error { return nil }

type noopWorkspaceManager struct{}

func (noopWorkspaceManager) Prepare(string) (ports.Workspace, error) { return noopWorkspace{}, nil }

type noopWorkspace struct{}

func (noopWorkspace) SourcePath(string) (string, error) { return "source", nil }
func (noopWorkspace) OutputPath(string) (string, error) { return "output", nil }
func (noopWorkspace) OutputDir() string                 { return "output" }
func (noopWorkspace) EnsureExists(string) error         { return nil }
func (noopWorkspace) Cleanup() error                    { return nil }

type noopCallback struct{}

func (noopCallback) Send(context.Context, string, ports.CallbackPayload) (bool, error) {
	return false, nil
}
