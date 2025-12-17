package app

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"preprocess-service/db/migrations"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib"

	"preprocess-service/internal/config"
	"preprocess-service/internal/observability"
	"preprocess-service/internal/storage/postgres"
	"preprocess-service/internal/transport"
	"preprocess-service/internal/worker"
)

type App struct {
	cfg        config.Config
	httpServer *transport.HTTPServer
	grpcServer *transport.GRPCServer
	worker     *worker.Worker
	db         *pgxpool.Pool
	telemetry  *observability.Telemetry
	logger     *slog.Logger
}

func New(cfg config.Config) (*App, error) {
	logger := newLogger(cfg.Logging.Level)

	telemetry, err := observability.Setup(context.Background(), cfg.Service.Name)
	if err != nil {
		return nil, fmt.Errorf("init telemetry: %w", err)
	}

	if err := runMigrations(context.Background(), cfg.Postgres.DSN, logger); err != nil {
		return nil, err
	}

	dbpool, err := pgxpool.New(context.Background(), cfg.Postgres.DSN)
	if err != nil {
		return nil, fmt.Errorf("connect postgres: %w", err)
	}

	repo := postgres.NewJobRepository(dbpool)
	handler := transport.NewPreprocessHandler(repo, cfg.Preprocess.TargetBucket, cfg.Preprocess.TargetKeyPrefix)

	grpcServer, err := transport.NewGRPCServer(cfg.Service.GRPCPort, handler, telemetry.Meter(), logger)
	if err != nil {
		dbpool.Close()
		return nil, err
	}

	workerCfg := worker.Config{
		LibreOfficePath: cfg.Preprocess.LibreOfficePath,
		WorkingDir:      cfg.Preprocess.WorkingDir,
		OutputDir:       cfg.Preprocess.OutputDir,
		TargetBucket:    cfg.Preprocess.TargetBucket,
		TargetMediaType: cfg.Preprocess.TargetMediaType,
		Concurrency:     cfg.Preprocess.WorkerConcurrency,
		ConvertTimeout:  cfg.Preprocess.ConvertTimeout,
		Backoff:         cfg.Preprocess.Backoff,
		PollInterval:    cfg.Preprocess.PollInterval,
	}

	wrk, err := worker.New(dbpool, cfg.Minio, workerCfg, logger)
	if err != nil {
		dbpool.Close()
		return nil, err
	}

	return &App{
		cfg:        cfg,
		httpServer: transport.NewHTTPServer(cfg.Service.Port, dbpool, telemetry.MetricsHandler, telemetry.Meter(), logger),
		grpcServer: grpcServer,
		worker:     wrk,
		db:         dbpool,
		telemetry:  telemetry,
		logger:     logger,
	}, nil
}

func (a *App) Run(ctx context.Context) error {
	fmt.Printf("starting %s on port %d (grpc: %d)\n", a.cfg.Service.Name, a.cfg.Service.Port, a.cfg.Service.GRPCPort)

	errCh := make(chan error, 3)

	go func() {
		errCh <- a.httpServer.Start(ctx)
	}()

	go func() {
		errCh <- a.grpcServer.Start(ctx)
	}()

	go func() {
		errCh <- a.worker.Run(ctx)
	}()

	for i := 0; i < cap(errCh); i++ {
		if err := <-errCh; err != nil && !errors.Is(err, context.Canceled) {
			return err
		}
	}

	return nil
}

func (a *App) Close() {
	if a.db != nil {
		a.db.Close()
	}

	if a.telemetry != nil {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := a.telemetry.Shutdown(shutdownCtx); err != nil {
			a.logger.Error("shutdown telemetry", "error", err)
		}
	}
}

func newLogger(level string) *slog.Logger {
	slogLevel := slog.LevelInfo
	switch strings.ToLower(level) {
	case "debug":
		slogLevel = slog.LevelDebug
	case "warn", "warning":
		slogLevel = slog.LevelWarn
	case "error":
		slogLevel = slog.LevelError
	}

	handler := slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slogLevel})
	return slog.New(handler)
}

func runMigrations(ctx context.Context, dsn string, logger *slog.Logger) error {
	sqlDB, err := sql.Open("pgx", dsn)
	if err != nil {
		return fmt.Errorf("open postgres connection: %w", err)
	}
	defer sqlDB.Close()

	if err := migrations.Up(ctx, sqlDB); err != nil {
		return fmt.Errorf("run migrations: %w", err)
	}

	logger.Info("migrations applied")
	return nil
}
