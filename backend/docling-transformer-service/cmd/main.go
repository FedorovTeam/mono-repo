package main

import (
	"context"
	"database/sql"
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"github.com/mono-repo/docling-transformer-service/db/migrations"
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
	configPath := flag.String("config", "", "path to config file")
	migrateOnly := flag.Bool("migrate-only", false, "apply migrations and exit")
	flag.Parse()

	cfg := config.MustLoadFromPath(resolveConfigPath(*configPath))

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

	db, err := sql.Open("pgx", cfg.Postgres.DSN)
	if err != nil {
		logger.Fatal("failed to open postgres connection", zap.Error(err))
	}
	defer db.Close()

	if cfg.Postgres.MaxConnections > 0 {
		db.SetMaxOpenConns(int(cfg.Postgres.MaxConnections))
	}

	if err := migrations.Run(ctx, db); err != nil {
		logger.Fatal("failed to run migrations", zap.Error(err))
	}

	if *migrateOnly {
		logger.Info("migrations applied, exiting due to -migrate-only flag")
		return
	}

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
		DefaultLanguage:    cfg.Docling.DefaultLanguage,
		PDFDefaultLanguage: cfg.Docling.PDFDefaultLanguage,
		PDFEnableOCR:       cfg.Docling.PDFEnableOCR,
	}, logger)

	workerProcessor := worker.NewProcessor(queueRepo, conv, storageClient, logger, worker.Config{
		MarkdownBucket:         cfg.Minio.Bucket,
		MaxWorkers:             cfg.Service.MaxWorkers,
		PollInterval:           cfg.Service.PollInterval,
		ProcessingTimeout:      cfg.Service.ProcessingTimeout,
		FastOperationTimeout:   cfg.Service.IOTimeout,
		MaxProcessingRetries:   cfg.Service.MaxProcessingRetries,
		CleanupInterval:        cfg.Service.CleanupInterval,
		CleanupRetention:       cfg.Service.RetentionPeriod,
		DoclingInitialInterval: cfg.Docling.StatusInitialInterval,
		DoclingMaxInterval:     cfg.Docling.StatusMaxInterval,
		DoclingBackoffFactor:   cfg.Docling.StatusBackoffFactor,
		DoclingMaxAttempts:     cfg.Docling.StatusMaxAttempts,
		DoclingMaxLifetime:     cfg.Docling.MaxJobLifetime,
	})
	workerProcessor.Run(ctx)

	if err := server.Run(ctx, logger, queueRepo, cfg.Minio.Bucket, cfg.Service.MaxWorkers, cfg.Service.GRPCPort, cfg.Observability); err != nil {
		logger.Fatal("server failed", zap.Error(err))
	}
}

func resolveConfigPath(flagPath string) string {
	if flagPath != "" {
		return flagPath
	}

	if envPath := os.Getenv("CONFIG_PATH"); envPath != "" {
		return envPath
	}

	return "config/config.yaml"
}
