package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/mono-repo/docling-transformer-service/internal/config"
	"github.com/mono-repo/docling-transformer-service/internal/converter"
	"github.com/mono-repo/docling-transformer-service/internal/server"
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

	conv := converter.NewPandocConverter(cfg.Pandoc.Endpoint, cfg.Docling.Endpoint)

	if err := server.Run(ctx, logger, conv, cfg.Service.GRPCPort); err != nil {
		logger.Fatal("server failed", zap.Error(err))
	}
}
