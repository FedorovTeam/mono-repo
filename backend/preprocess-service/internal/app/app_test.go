package app

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"preprocess-service/internal/config"
	"preprocess-service/internal/storage/memory"
	"preprocess-service/internal/usecase/ports"
)

func TestNewAppWithoutTelemetry(t *testing.T) {
	cfg := config.Config{
		Service: config.ServiceConfig{
			Name:     "preprocess-service",
			Port:     0,
			GRPCPort: 0,
		},
		Preprocess: config.PreprocessConfig{
			LibreOfficePath:   "/usr/bin/soffice",
			WorkingDir:        t.TempDir(),
			OutputDir:         t.TempDir(),
			SourceBucket:      "source",
			TargetBucket:      "target",
			TargetKeyPrefix:   "converted",
			TargetMediaType:   "application/pdf",
			WorkerConcurrency: 1,
			ConvertTimeout:    time.Second,
			Backoff:           time.Second,
			PollInterval:      10 * time.Millisecond,
		},
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

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
