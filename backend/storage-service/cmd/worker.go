package main

import (
	"context"
	"fmt"
	"log"

	"github.com/jackc/pgx/v5/pgxpool"

	"backend-storage-service/config"
	"backend-storage-service/internal/application"
	"backend-storage-service/internal/infrastructure/persistence"
)

func startProcessingWorker(ctx context.Context, cfg config.ProcessingConfig) (*pgxpool.Pool, error) {
	if cfg.DSN == "" {
		return nil, fmt.Errorf("processing.dsn is required")
	}

	pool, err := pgxpool.New(ctx, cfg.DSN)
	if err != nil {
		return nil, fmt.Errorf("connect db: %w", err)
	}

	txManager := persistence.NewTxManager(pool)
	jobRepo := persistence.NewProcessingJobRepository()
	documentRepo := persistence.NewDocumentRepository()

	workerCfg := application.WorkerConfig{
		Stage:        cfg.Stage,
		PollInterval: cfg.PollInterval(),
		MaxAttempts:  cfg.MaxAttempts,
		Backoff:      cfg.Backoff(),
	}
	service := application.NewWorkerService(workerCfg, txManager, jobRepo, documentRepo, log.Default())

	go func() {
		log.Printf("starting processing worker for stage=%s", workerCfg.Stage)
		service.Run(ctx)
		log.Printf("processing worker stopped: stage=%s", workerCfg.Stage)
	}()

	return pool, nil
}
