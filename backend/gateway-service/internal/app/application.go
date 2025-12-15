package app

import (
	"backend-gateway-service/internal/config"
	"backend-gateway-service/internal/domain"
	docclient "backend-gateway-service/internal/infrastructure/clients/document"
	storageclient "backend-gateway-service/internal/infrastructure/clients/storage"
	"backend-gateway-service/internal/server"
	"backend-gateway-service/pkg/grpcdial"
	"backend-gateway-service/pkg/logging"
	"backend-gateway-service/pkg/observability"
	"context"
	"fmt"
	"sync"

	"go.uber.org/zap"
)

type applicationOptions struct {
	logger         *zap.Logger
	providers      observability.Providers
	shutdownOTEL   func(context.Context) error
	documentClient domain.DocumentClient
	storageClient  domain.StorageClient
}

// Option позволяет переопределять зависимости приложения.
type Option func(*applicationOptions)

// WithLogger задаёт кастомный логгер.
func WithLogger(logger *zap.Logger) Option {
	return func(opts *applicationOptions) {
		opts.logger = logger
	}
}

// WithDocumentClient задаёт реализацию клиента document-service.
func WithDocumentClient(client domain.DocumentClient) Option {
	return func(opts *applicationOptions) {
		opts.documentClient = client
	}
}

// WithStorageClient задаёт реализацию клиента storage-service.
func WithStorageClient(client domain.StorageClient) Option {
	return func(opts *applicationOptions) {
		opts.storageClient = client
	}
}

// WithObservability позволяет переопределить провайдеры телеметрии и функцию остановки.
func WithObservability(providers observability.Providers, shutdown func(context.Context) error) Option {
	return func(opts *applicationOptions) {
		opts.providers = providers
		opts.shutdownOTEL = shutdown
	}
}

// Application инкапсулирует сборку зависимостей и управление сервером.
type Application struct {
	cfg      config.Config
	server   *server.Server
	cancel   context.CancelFunc
	done     chan struct{}
	runErr   error
	runOnce  sync.Once
	stopOnce sync.Once
	started  chan struct{}
}

// New создаёт приложение, собирая все зависимости согласно конфигурации.
func New(configPath string, opts ...Option) (*Application, error) {
	cfg, err := config.Load(configPath)
	if err != nil {
		return nil, fmt.Errorf("cannot load config: %w", err)
	}

	options := applicationOptions{}
	for _, opt := range opts {
		opt(&options)
	}

	logger := options.logger
	if logger == nil {
		logger, err = logging.New(cfg.Logging.Level)
		if err != nil {
			return nil, fmt.Errorf("failed to init logger: %w", err)
		}
	}

	providers := options.providers
	shutdownOTEL := options.shutdownOTEL
	if providers.TracerProvider == nil && providers.MeterProvider == nil && shutdownOTEL == nil {
		providers, shutdownOTEL, err = observability.Setup(context.Background(), cfg.Observability.EnabledOrDefault(), cfg.Observability.OTLPEndpoint, cfg.Observability.ServiceName)
		if err != nil {
			return nil, fmt.Errorf("failed to init telemetry: %w", err)
		}
	}
	if shutdownOTEL == nil {
		shutdownOTEL = func(context.Context) error { return nil }
	}

	documentClient := options.documentClient
	if documentClient == nil {
		dialOpts := grpcdial.DefaultOptions(cfg.DocumentService.GRPC, providers)
		docDialCtx, cancelDocDial := context.WithTimeout(context.Background(), cfg.DocumentService.GRPC.DialTimeout)
		defer cancelDocDial()

		documentClient, err = docclient.NewGRPCClient(docDialCtx, cfg.DocumentService.Address, dialOpts...)
		if err != nil {
			return nil, fmt.Errorf("failed to init document client: %w", err)
		}
	}

	storageClient := options.storageClient
	if storageClient == nil {
		storageDialOpts := grpcdial.DefaultOptions(cfg.StorageService.GRPC, providers)
		storageDialCtx, cancelStorageDial := context.WithTimeout(context.Background(), cfg.StorageService.GRPC.DialTimeout)
		defer cancelStorageDial()

		storageClient, err = storageclient.NewGRPCClient(storageDialCtx, cfg.StorageService.Address, storageDialOpts...)
		if err != nil {
			return nil, fmt.Errorf("failed to init storage client: %w", err)
		}
	}

	srv, err := server.New(cfg, server.Dependencies{
		Logger:         logger,
		DocumentClient: documentClient,
		StorageClient:  storageClient,
		Providers:      providers,
		ShutdownOTEL:   shutdownOTEL,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to init server: %w", err)
	}

	return &Application{cfg: cfg, server: srv, started: make(chan struct{})}, nil
}

// Run запускает сервер и блокируется до завершения или отмены контекста.
func (a *Application) Run(ctx context.Context) error {
	a.runOnce.Do(func() {
		var runCtx context.Context
		runCtx, a.cancel = context.WithCancel(ctx)
		a.done = make(chan struct{})
		close(a.started)

		go func() {
			defer close(a.done)
			a.runErr = a.server.Run(runCtx)
		}()
	})

	if a.done == nil {
		return fmt.Errorf("application is not initialized")
	}

	select {
	case <-a.done:
		return a.runErr
	case <-ctx.Done():
		return a.Stop(context.Background())
	}
}

// Stop завершает работу сервера и ожидает его остановки.
func (a *Application) Stop(ctx context.Context) error {
	select {
	case <-a.started:
	case <-ctx.Done():
		return ctx.Err()
	}

	if a.cancel == nil || a.done == nil {
		return nil
	}

	a.stopOnce.Do(func() {
		a.cancel()
	})

	select {
	case <-a.done:
		return a.runErr
	case <-ctx.Done():
		return ctx.Err()
	}
}
