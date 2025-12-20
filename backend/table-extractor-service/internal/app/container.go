package app

import (
	"context"
	"database/sql"
	"errors"

	"github.com/minio/minio-go/v7"
	"github.com/mono-repo/table-extractor-service/db/migrations"
	"github.com/mono-repo/table-extractor-service/internal/config"
	"github.com/mono-repo/table-extractor-service/internal/repository"
	"github.com/mono-repo/table-extractor-service/internal/repository/sqlrepo"
	"github.com/mono-repo/table-extractor-service/internal/server"
	"github.com/mono-repo/table-extractor-service/internal/storage/database"
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

	deps := usecase.Dependencies{
		Queue:     repo,
		Storage:   storageClient,
		Extractor: nil,
	}

	uc := usecase.NewTableExtractorUseCase(deps, usecase.Config{
		QueueLimit:             cfg.Service.QueueLimit,
		DefaultArtifactsBucket: cfg.Minio.ArtifactsBucket,
		DefaultArtifactsPrefix: cfg.Minio.DefaultArtifactsPrefix,
	}, logger)

	grpcServer := server.NewGRPCServer(logger, uc, cfg.Service.Port, tracerProvider)

	var processor worker.Processor
	if deps.Extractor != nil {
		processor = uc
	}

	workerRunner := worker.NewRunner(worker.Config{
		MaxWorkers:        cfg.Service.MaxWorkers,
		ProcessingTimeout: cfg.Service.ProcessingTimeout,
		CleanupRetention:  cfg.Service.CleanupRetention,
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
