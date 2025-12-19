// backend/docling-transformer-service/cmd/main.go
package main

import (
	"context"
	"database/sql"
	"flag"
	"log"
	"net/http"
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
	"github.com/mono-repo/docling-transformer-service/internal/infrastructure/converter"
	"github.com/mono-repo/docling-transformer-service/internal/infrastructure/repository"
	"github.com/mono-repo/docling-transformer-service/internal/infrastructure/storage"
	"github.com/mono-repo/docling-transformer-service/internal/infrastructure/telemetry"
	grpcapi "github.com/mono-repo/docling-transformer-service/internal/interfaces/grpc"
	"github.com/mono-repo/docling-transformer-service/internal/usecase"
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

	restored, err := queueRepo.RestoreProcessing(ctx)
	if err != nil {
		logger.Fatal("failed to restore processing tasks", zap.Error(err))
	}
	logger.Info("restored processing tasks", zap.Int("count", restored))

	statusPolicy := cfg.StatusBackoffPolicy()

	// --- OCR converter (обычный) ---
	convOCR := converter.NewDoclingConverter(converter.Config{
		Endpoint:           cfg.Docling.Endpoint,
		APIKey:             cfg.Docling.APIKey,
		DefaultLanguage:    cfg.Docling.DefaultLanguage,
		PDFDefaultLanguage: cfg.Docling.PDFDefaultLanguage,
		PDFEnableOCR:       cfg.Docling.PDFEnableOCR,
		PDFBackend:         cfg.Docling.PDFBackend,
		Mode:               cfg.Docling.Mode,
		TraceHttpRequest:   cfg.Observability.LoggingEnabled,
		Storage: converter.StorageConfig{
			Endpoint:  cfg.Docling.Storage.Endpoint,
			AccessKey: cfg.Docling.Storage.AccessKey,
			SecretKey: cfg.Docling.Storage.SecretKey,
			Bucket:    cfg.Docling.Storage.Bucket,
			Region:    cfg.Docling.Storage.Region,
			UseSSL:    cfg.Docling.Storage.UseSSL,
		},
		Render: converter.RenderConfig{
			PreferJSON:         cfg.DoclingRender.PreferJSON != nil && *cfg.DoclingRender.PreferJSON,
			TwoPassVotingMerge: cfg.DoclingRender.TwoPassVotingMerge,
			VotingTableMinRows: cfg.DoclingRender.VotingTableMinRows,
			PassBTableCoverage: cfg.DoclingRender.PassBTableCoverage,
			PassBMatchRate:     0.6,
		},
	}, logger)

	transformUC := usecase.NewTransformerService(queueRepo, convOCR, usecase.TransformationConfig{
		MarkdownBucket:    cfg.Minio.Bucket,
		QueueLimit:        cfg.Service.QueueLimit,
		StatusBackoff:     statusPolicy,
		UseSourceAsync:    cfg.Docling.Mode == "source_async",
		DefaultOCRLang:    cfg.Docling.PDFDefaultLanguage,
		DefaultPDFBackend: cfg.Docling.PDFBackend,
	}, logger, &http.Client{Timeout: 30 * time.Second})

	workerProcessor := usecase.NewProcessor(queueRepo, convOCR, storageClient, logger, usecase.Config{
		MarkdownBucket:         cfg.Minio.Bucket,
		MaxWorkers:             cfg.Service.MaxWorkers,
		PollInterval:           cfg.Scheduler.PollInterval,
		BatchSize:              cfg.Scheduler.BatchSize,
		ProcessingTimeout:      cfg.Service.ProcessingTimeout,
		FastOperationTimeout:   cfg.Service.IOTimeout,
		MaxProcessingRetries:   cfg.Scheduler.MaxProcessingRetries,
		RetryBackoff:           cfg.Scheduler.RetryBackoff,
		SubmitLockDelay:        cfg.Scheduler.RetryBackoff,
		CleanupInterval:        cfg.Scheduler.MaintenanceInterval,
		CleanupRetention:       cfg.Scheduler.CleanupRetention,
		FailedRetryDelay:       cfg.Scheduler.FailedRetryDelay,
		MaxFailedRetries:       cfg.Scheduler.MaxFailedRetries,
		DoclingInitialInterval: cfg.Docling.StatusInitialInterval,
		DoclingMaxInterval:     cfg.Docling.StatusMaxInterval,
		DoclingBackoffFactor:   cfg.Docling.StatusBackoffFactor,
		DoclingMaxAttempts:     cfg.Docling.StatusMaxAttempts,
		DoclingMaxLifetime:     cfg.Docling.MaxJobLifetime,
		DoclingRequeueDelay:    cfg.Scheduler.DoclingRequeueDelay,
		DoclingMaxRestarts:     cfg.Scheduler.DoclingMaxRestarts,
		UseSourceAsync:         cfg.Docling.Mode == "source_async",
		DefaultOCRLang:         cfg.Docling.PDFDefaultLanguage,
		DefaultPDFBackend:      cfg.Docling.PDFBackend,
	})

	workerDone := make(chan struct{})
	go func() {
		workerProcessor.Run(ctx)
		close(workerDone)
	}()
	logger.Info("worker processor started")

	if err := grpcapi.Run(ctx, logger, transformUC, cfg.Service.GRPCPort); err != nil {
		stop()
		<-workerDone
		logger.Fatal("server failed", zap.Error(err))
	}

	stop()
	<-workerDone
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
