package app

import (
	"context"
	"fmt"

	"preprocess-service/internal/config"
	"preprocess-service/internal/storage/memory"
	"preprocess-service/internal/transport"
)

type App struct {
	cfg        config.Config
	httpServer *transport.HTTPServer
	grpcServer *transport.GRPCServer
}

func New(cfg config.Config) (*App, error) {
	repo := memory.NewJobRepository()
	handler := transport.NewPreprocessHandler(repo)

	grpcServer, err := transport.NewGRPCServer(cfg.Service.GRPCPort, handler)
	if err != nil {
		return nil, err
	}

	return &App{cfg: cfg, httpServer: transport.NewHTTPServer(cfg.Service.Port), grpcServer: grpcServer}, nil
}

func (a *App) Run(ctx context.Context) error {
	fmt.Printf("starting %s on port %d (grpc: %d)\n", a.cfg.Service.Name, a.cfg.Service.Port, a.cfg.Service.GRPCPort)

	errCh := make(chan error, 2)

	go func() {
		errCh <- a.httpServer.Start(ctx)
	}()

	go func() {
		errCh <- a.grpcServer.Start(ctx)
	}()

	for i := 0; i < 2; i++ {
		if err := <-errCh; err != nil {
			return err
		}
	}

	return nil
}
