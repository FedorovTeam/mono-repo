package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/mono-repo/table-extractor-service/internal/app"
	"go.uber.org/zap"
)

func main() {
	configPath := flag.String("config", "", "Path to config file")
	flag.Parse()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	container, err := app.Build(ctx, *configPath)
	if err != nil {
		log.Fatalf("failed to build container: %v", err)
	}
	defer func() {
		_ = container.Logger.Sync()
	}()
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if container.Cleanup != nil {
			_ = container.Cleanup(shutdownCtx)
		}
	}()

	if err := container.Server.Run(ctx); err != nil {
		container.Logger.Fatal("server failed", zap.Error(err))
	}
}
