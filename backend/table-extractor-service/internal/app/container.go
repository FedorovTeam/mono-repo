package app

import (
	"context"

	"github.com/mono-repo/table-extractor-service/internal/config"
	"github.com/mono-repo/table-extractor-service/internal/repository"
	"github.com/mono-repo/table-extractor-service/internal/repository/memory"
	"github.com/mono-repo/table-extractor-service/internal/server"
	"github.com/mono-repo/table-extractor-service/internal/telemetry"
	"github.com/mono-repo/table-extractor-service/internal/usecase"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
)

type Container struct {
	Config         *config.Config
	Logger         *zap.Logger
	TracerProvider trace.TracerProvider
	Repository     repository.TableExtractionRepository
	UseCase        *usecase.TableExtractorUseCase
	Server         *server.GRPCServer
	Cleanup        func(context.Context) error
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

	repo := memory.NewInMemoryRepository()

	uc := usecase.NewTableExtractorUseCase(repo, usecase.Config{
		QueueLimit:             cfg.Service.QueueLimit,
		DefaultArtifactsBucket: cfg.Storage.ArtifactsBucket,
		DefaultArtifactsPrefix: cfg.Service.DefaultArtifactsPrefix,
	}, logger)

	grpcServer := server.NewGRPCServer(logger, uc, cfg.Service.GRPCPort, tracerProvider)

	return &Container{
		Config:         cfg,
		Logger:         logger,
		TracerProvider: tracerProvider,
		Repository:     repo,
		UseCase:        uc,
		Server:         grpcServer,
		Cleanup:        shutdownTracer,
	}, nil
}
