package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"

	"preprocess-service/cmd/bootstrap"
	"preprocess-service/internal/app"
	"preprocess-service/internal/config"
)

func main() {
	cfgPath := os.Getenv("CONFIG_PATH")
	cfg, err := config.Load(cfgPath)
	if err != nil {
		log.Fatalf("failed to load config: %v", err)
	}

	zapLogger, _, err := app.NewLogger(cfg.Logging)
	if err != nil {
		log.Fatalf("failed to init logger: %v", err)
	}
	defer func() { _ = zapLogger.Sync() }()
	initCtx := context.Background()

	if err := bootstrap.RunMigrations(initCtx, cfg.Postgres.DSN, zapLogger); err != nil {
		log.Fatalf("failed to run migrations: %v", err)
	}

	deps, err := bootstrap.BuildDependencies(initCtx, cfg, zapLogger)
	if err != nil {
		log.Fatalf("failed to init dependencies: %v", err)
	}

	application, err := app.New(cfg, deps)
	if err != nil {
		log.Fatalf("failed to init app: %v", err)
	}
	defer application.Close()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if err := application.Run(ctx); err != nil {
		log.Fatalf("application stopped with error: %v", err)
	}
}
