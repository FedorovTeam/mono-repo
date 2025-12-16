package main

import (
    "context"
    "log"
    "os"
    "os/signal"
    "syscall"

    "preprocess-service/internal/app"
    "preprocess-service/internal/config"
)

func main() {
    cfgPath := os.Getenv("CONFIG_PATH")
    cfg, err := config.Load(cfgPath)
    if err != nil {
        log.Fatalf("failed to load config: %v", err)
    }

    application := app.New(cfg)

    ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
    defer stop()

    if err := application.Run(ctx); err != nil {
        log.Fatalf("application stopped with error: %v", err)
    }
}
