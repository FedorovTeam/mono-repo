package app

import (
	"context"
	"database/sql"
	"errors"

	"github.com/minio/minio-go/v7"
	"github.com/mono-repo/table-extractor-service/db/migrations"
	"github.com/mono-repo/table-extractor-service/internal/config"
	"github.com/mono-repo/table-extractor-service/internal/extractor"
	"github.com/mono-repo/table-extractor-service/internal/repository"
	"github.com/mono-repo/table-extractor-service/internal/repository/sqlrepo"
	"github.com/mono-repo/table-extractor-service/internal/server"
	"github.com/mono-repo/table-extractor-service/internal/storage/database"
	"github.com/mono-repo/table-extractor-service/internal/storage/filecache"
	objectstorage "github.com/mono-repo/table-extractor-service/internal/storage/object"
	"github.com/mono-repo/table-extractor-service/internal/telemetry"
	"github.com/mono-repo/table-extractor-service/internal/usecase"
	"github.com/mono-repo/table-extractor-service/internal/worker"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
)

type Container struct {
	Config         *config.Config
	Logger         *zap.Logger
	TracerProvider trace.TracerProvider
	MinioClient    *minio.Client
	DB             *sql.DB
	Repository     repository.TableExtractionRepository
	UseCase        *usecase.TableExtractorUseCase
	Server         *server.GRPCServer
	Worker         *worker.Runner
	Cleanup        func(ctx context.Context) error
}

func Build(ctx context.Context, configPath string) (*Container, error) {
	cfg := config.MustLoad(configPath)

	logger, err := telemetry.NewLogger(cfg.Observability.LoggingEnabled)
	if err != nil {
		return nil, err
	}

	tracerProvider, shutdownTracer, err := telemetry.SetupTracer(cfg.Service.Name, cfg.Observability.Tracing.Enabled)
	if err != nil {
		return nil, err
	}

	minioClient, err := objectstorage.NewMinioClient(cfg.Minio, cfg.Storage)
	if err != nil {
		return nil, err
	}

	db, dialect, err := database.New(ctx, cfg.Database)
	if err != nil {
		return nil, err
	}

	if err := migrations.Apply(ctx, db, logger); err != nil {
		return nil, err
	}

	repo := sqlrepo.NewSQLRepository(db, dialect)

	storageClient := objectstorage.NewMinioStorage(minioClient, cfg.Storage)
	fileCache := filecache.NewTempCache(storageClient)

	extractAdapter := extractor.NewAdapter(extractor.Config{
		PdfInfoPath:         cfg.Extractor.PdfInfoPath,
		PdftotextPath:       cfg.Extractor.PdftotextPath,
		PdftoppmPath:        cfg.Extractor.PdftoppmPath,
		PdfInfoTimeout:      cfg.Extractor.PdfInfoTimeout,
		PdftotextTimeout:    cfg.Extractor.PdftotextTimeout,
		PdftoppmTimeout:     cfg.Extractor.PdftoppmTimeout,
		PdftoppmDPI:         cfg.Extractor.PdftoppmDPI,
		MaxParallelCommands: cfg.Extractor.MaxParallel,
		Pages:               cfg.Extractor.Pages,
		DetectVotingTables:  cfg.Extractor.DetectVotingTables,
		TempDir:             cfg.Extractor.TempDir,
		Mode:                cfg.Extractor.Mode,
	})

	deps := usecase.Dependencies{
		Queue:     repo,
		Storage:   storageClient,
		FileCache: fileCache,
		Extractor: extractAdapter,
	}

	uc := usecase.NewTableExtractorUseCase(deps, usecase.Config{
		QueueLimit:             cfg.Service.QueueLimit,
		DefaultArtifactsBucket: cfg.Minio.ArtifactsBucket,
		DefaultArtifactsPrefix: cfg.Minio.DefaultArtifactsPrefix,
		AcquireBatchSize:       cfg.Service.BatchSize,
	}, logger)

	grpcServer := server.NewGRPCServer(logger, uc, cfg.Service.Port, tracerProvider, nil)

	processor := worker.Processor(uc)

	workerRunner := worker.NewRunner(worker.Config{
		BatchSize:           cfg.Service.BatchSize,
		MaxWorkers:          cfg.Service.MaxWorkers,
		ProcessingTimeout:   cfg.Service.ProcessingTimeout,
		CleanupRetention:    cfg.Service.CleanupRetention,
		MaintenanceInterval: cfg.Service.ProcessingTimeout / 5,
		Retry: worker.RetryConfig{
			MaxAttempts:     cfg.Service.Retry.MaxAttempts,
			BackoffInterval: cfg.Service.Retry.BackoffInterval,
			MaxBackoff:      cfg.Service.Retry.MaxBackoff,
		},
	}, processor, logger)

	cleanup := func(ctx context.Context) error {
		var result error
		if shutdownTracer != nil {
			result = errors.Join(result, shutdownTracer(ctx))
		}
		if db != nil {
			result = errors.Join(result, db.Close())
		}
		return result
	}

	return &Container{
		Config:         cfg,
		Logger:         logger,
		TracerProvider: tracerProvider,
		MinioClient:    minioClient,
		DB:             db,
		Repository:     repo,
		UseCase:        uc,
		Server:         grpcServer,
		Worker:         workerRunner,
		Cleanup:        cleanup,
	}, nil
}
