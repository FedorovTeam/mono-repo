package bootstrap

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib"

	"preprocess-service/db/migrations"
	"preprocess-service/internal/adapter/libreoffice"
	"preprocess-service/internal/adapter/workspace"
	"preprocess-service/internal/app"
	"preprocess-service/internal/config"
	"preprocess-service/internal/observability"
	storageminio "preprocess-service/internal/storage/minio"
	"preprocess-service/internal/storage/postgres"
	adaptercallback "preprocess-service/internal/transport/callback"
)

// RunMigrations запускает встроенные миграции до старта сервиса.
func RunMigrations(ctx context.Context, dsn string, logger *slog.Logger) error {
	sqlDB, err := sql.Open("pgx", dsn)
	if err != nil {
		return fmt.Errorf("open postgres connection: %w", err)
	}
	defer sqlDB.Close()

	if err := migrations.Up(ctx, sqlDB); err != nil {
		return fmt.Errorf("run migrations: %w", err)
	}

	if logger != nil {
		logger.Info("migrations applied")
	}

	return nil
}

// BuildDependencies создаёт инфраструктурные зависимости приложения.
func BuildDependencies(ctx context.Context, cfg config.Config, logger *slog.Logger) (app.Dependencies, error) {
	if logger == nil {
		logger = slog.Default()
	}

	telemetry, err := observability.Setup(ctx, cfg.Service.Name)
	if err != nil {
		return app.Dependencies{}, fmt.Errorf("init telemetry: %w", err)
	}

	dbpool, err := pgxpool.New(ctx, cfg.Postgres.DSN)
	if err != nil {
		shutdownTelemetry(ctx, telemetry, logger)
		return app.Dependencies{}, fmt.Errorf("connect postgres: %w", err)
	}

	repo := postgres.NewPreprocessRepository(dbpool)

	minioClient, err := storageminio.NewClient(cfg.Minio)
	if err != nil {
		dbpool.Close()
		shutdownTelemetry(ctx, telemetry, logger)
		return app.Dependencies{}, fmt.Errorf("init minio client: %w", err)
	}
	storageAdapter := storageminio.NewAdapter(minioClient)

	converter, err := libreoffice.NewExecConverter(cfg.Preprocess.LibreOfficePath)
	if err != nil {
		dbpool.Close()
		shutdownTelemetry(ctx, telemetry, logger)
		return app.Dependencies{}, fmt.Errorf("init libreoffice converter: %w", err)
	}

	workspaceManager, err := workspace.NewFSManager(cfg.Preprocess.WorkingDir, cfg.Preprocess.OutputDir)
	if err != nil {
		dbpool.Close()
		shutdownTelemetry(ctx, telemetry, logger)
		return app.Dependencies{}, fmt.Errorf("init workspace manager: %w", err)
	}

	callbackClient := adaptercallback.NewClient(nil)

	return app.Dependencies{
		Logger:           logger,
		Telemetry:        telemetry,
		DB:               dbpool,
		Repo:             repo,
		Downloader:       storageAdapter,
		Uploader:         storageAdapter,
		Converter:        converter,
		WorkspaceManager: workspaceManager,
		Callback:         callbackClient,
	}, nil
}

func shutdownTelemetry(ctx context.Context, telemetry *observability.Telemetry, logger *slog.Logger) {
	if telemetry == nil {
		return
	}

	shutdownCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	if err := telemetry.Shutdown(shutdownCtx); err != nil && logger != nil {
		logger.Error("shutdown telemetry after bootstrap failure", "error", err)
	}
}
