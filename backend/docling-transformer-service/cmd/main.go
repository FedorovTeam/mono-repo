package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"github.com/mono-repo/docling-transformer-service/internal/config"
	"github.com/mono-repo/docling-transformer-service/internal/converter"
	"github.com/mono-repo/docling-transformer-service/internal/server"
	"github.com/mono-repo/docling-transformer-service/internal/storage"
	"go.uber.org/zap"
)

func main() {
	cfg := config.MustLoad()

	logger, err := zap.NewProduction()
	if err != nil {
		log.Fatalf("failed to init logger: %v", err)
	}
	defer logger.Sync()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

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

	if err := server.Run(ctx, logger, conv, storageClient, cfg.Minio.Bucket, cfg.Service.GRPCPort); err != nil {
		logger.Fatal("server failed", zap.Error(err))
	}
}
