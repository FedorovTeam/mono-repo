package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"github.com/mono-repo/docling-transformer-service/internal/config"
	"github.com/mono-repo/docling-transformer-service/internal/converter"
	"github.com/mono-repo/docling-transformer-service/internal/server"
	"github.com/mono-repo/docling-transformer-service/internal/storage"
	"github.com/mono-repo/docling-transformer-service/internal/telemetry"
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
	conv := converter.NewDoclingConverter(cfg.Docling.Endpoint, logger)

	if err := server.Run(ctx, logger, conv, storageClient, cfg.Minio.Bucket, cfg.Service.GRPCPort, cfg.Observability); err != nil {
		logger.Fatal("server failed", zap.Error(err))
	}
}
