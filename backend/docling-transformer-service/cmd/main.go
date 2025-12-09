package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"github.com/mono-repo/docling-transformer-service/internal/config"
	"github.com/mono-repo/docling-transformer-service/internal/converter"
	"github.com/mono-repo/docling-transformer-service/internal/repository"
	"github.com/mono-repo/docling-transformer-service/internal/server"
	"github.com/mono-repo/docling-transformer-service/internal/storage"
	"github.com/mono-repo/docling-transformer-service/internal/telemetry"
	"github.com/mono-repo/docling-transformer-service/internal/worker"
	"go.uber.org/zap"
)

func main() {
	cfg := config.MustLoad()

	logger, err := telemetry.NewLogger(cfg.Observability.LoggingEnabled)
	if err != nil {
		log.Fatalf("failed to init logger: %v", err)
	}
	defer logger.Sync()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	shutdownObservability, err := telemetry.SetupProviders(ctx, cfg.Observability, cfg.Service.Name, logger)
	if err != nil {
		logger.Fatal("failed to setup observability", zap.Error(err))
	}
	defer telemetry.ShutdownWithTimeout(context.Background(), 5*time.Second, logger, shutdownObservability)

	minioClient, err := minio.New(cfg.Minio.Endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(cfg.Minio.AccessKey, cfg.Minio.SecretKey, ""),
		Secure: cfg.Minio.UseSSL,
		Region: cfg.Minio.Region,
	})
	if err != nil {
		logger.Fatal("failed to init minio", zap.Error(err))
	}

	storageClient := storage.NewMinioStorage(minioClient)
	poolConfig, err := pgxpool.ParseConfig(cfg.Postgres.DSN)
	if err != nil {
		logger.Fatal("failed to parse postgres dsn", zap.Error(err))
	}
	if cfg.Postgres.MaxConnections > 0 {
		poolConfig.MaxConns = cfg.Postgres.MaxConnections
	}

	pool, err := pgxpool.NewWithConfig(ctx, poolConfig)
	if err != nil {
		logger.Fatal("failed to init postgres", zap.Error(err))
	}
	defer pool.Close()

	queueRepo := repository.NewQueueRepository(pool)
	conv := converter.NewDoclingConverter(converter.Config{
		Endpoint:           cfg.Docling.Endpoint,
		APIKey:             cfg.Docling.APIKey,
		StatusPollInterval: cfg.Docling.StatusPollInterval,
		StatusPollTimeout:  cfg.Docling.StatusPollTimeout,
		StatusMaxAttempts:  cfg.Docling.StatusMaxAttempts,
	}, logger)

	workerProcessor := worker.NewProcessor(queueRepo, conv, storageClient, logger, worker.Config{
		MarkdownBucket:       cfg.Minio.Bucket,
		MaxWorkers:           cfg.Service.MaxWorkers,
		PollInterval:         cfg.Service.PollInterval,
		ProcessingTimeout:    cfg.Service.ProcessingTimeout,
		MaxProcessingRetries: cfg.Service.MaxProcessingRetries,
		CleanupInterval:      cfg.Service.CleanupInterval,
		CleanupRetention:     cfg.Service.RetentionPeriod,
		DoclingPollInterval:  cfg.Docling.StatusPollInterval,
		DoclingPollTimeout:   cfg.Docling.StatusPollTimeout,
		DoclingMaxAttempts:   cfg.Docling.StatusMaxAttempts,
	})
	workerProcessor.Run(ctx)

	if err := server.Run(ctx, logger, queueRepo, cfg.Minio.Bucket, cfg.Service.MaxWorkers, cfg.Service.GRPCPort, cfg.Observability); err != nil {
		logger.Fatal("server failed", zap.Error(err))
	}
}
