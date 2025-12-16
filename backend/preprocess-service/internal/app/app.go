package app

import (
    "context"
    "fmt"

    "preprocess-service/internal/config"
    "preprocess-service/internal/transport"
)

type App struct {
    cfg    config.Config
    server *transport.HTTPServer
}

func New(cfg config.Config) *App {
    return &App{cfg: cfg, server: transport.NewHTTPServer(cfg.Service.Port)}
}

func (a *App) Run(ctx context.Context) error {
    fmt.Printf("starting %s on port %d\n", a.cfg.Service.Name, a.cfg.Service.Port)
    return a.server.Start(ctx)
}
