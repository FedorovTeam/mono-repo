package main

import (
	"context"
	"errors"
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/mono-repo/table-extractor-service/internal/app"
	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"
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
			if err := container.Cleanup(shutdownCtx); err != nil {
				container.Logger.Warn("cleanup finished with errors", zap.Error(err))
			}
		}
	}()

	group, groupCtx := errgroup.WithContext(ctx)
	group.Go(func() error {
		return container.Server.Run(groupCtx)
	})
	group.Go(func() error {
		if container.Worker == nil {
			return nil
		}
		return container.Worker.Start(groupCtx)
	})

	if err := group.Wait(); err != nil && !errors.Is(err, context.Canceled) {
		container.Logger.Fatal("server failed", zap.Error(err))
	}
}
