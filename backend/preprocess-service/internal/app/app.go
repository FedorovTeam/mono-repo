package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"go.opentelemetry.io/otel/metric"
	otelmetric "go.opentelemetry.io/otel/metric/noop"

	"preprocess-service/internal/config"
	"preprocess-service/internal/domain"
	"preprocess-service/internal/observability"
	"preprocess-service/internal/transport"
	"preprocess-service/internal/usecase/ports"
	"preprocess-service/internal/usecase/submit"
	"preprocess-service/internal/worker"
)

// Dependencies описывает внешний слой и инфраструктуру, необходимую приложению.
type Dependencies struct {
	Logger           *slog.Logger
	Telemetry        *observability.Telemetry
	DB               *pgxpool.Pool
	Repo             domain.PreprocessJobRepository
	Downloader       ports.StorageDownloader
	Uploader         ports.StorageUploader
	Converter        ports.DocumentConverter
	WorkspaceManager ports.WorkspaceManager
	Callback         ports.CallbackSender
}

type App struct {
	cfg        config.Config
	httpServer *transport.HTTPServer
	grpcServer *transport.GRPCServer
	worker     *worker.Worker
	db         *pgxpool.Pool
	telemetry  *observability.Telemetry
	logger     *slog.Logger
}

func New(cfg config.Config, deps Dependencies) (*App, error) {
	logger := deps.Logger
	if logger == nil {
		logger = slog.Default()
	}

	if deps.Repo == nil {
		return nil, fmt.Errorf("job repository is required")
	}
	if deps.Downloader == nil {
		return nil, fmt.Errorf("storage downloader is required")
	}
	if deps.Uploader == nil {
		return nil, fmt.Errorf("storage uploader is required")
	}
	if deps.Converter == nil {
		return nil, fmt.Errorf("document converter is required")
	}
	if deps.WorkspaceManager == nil {
		return nil, fmt.Errorf("workspace manager is required")
	}
	if deps.Callback == nil {
		return nil, fmt.Errorf("callback sender is required")
	}

	submitService, err := submit.NewService(deps.Repo, cfg.Preprocess.TargetBucket, cfg.Preprocess.TargetKeyPrefix)
	if err != nil {
		return nil, fmt.Errorf("init submit service: %w", err)
	}
	handler := transport.NewPreprocessHandler(submitService)

	var (
		metricsHandler http.Handler
		meter          metric.Meter
	)

	if deps.Telemetry != nil {
		meter = deps.Telemetry.Meter()
		metricsHandler = deps.Telemetry.MetricsHandler
	} else {
		meter = otelmetric.NewMeterProvider().Meter("preprocess-service")
	}

	grpcServer, err := transport.NewGRPCServer(cfg.Service.GRPCPort, handler, meter, logger)
	if err != nil {
		return nil, err
	}

	workerCfg := worker.Config{
		LibreOfficePath: cfg.Preprocess.LibreOfficePath,
		WorkingDir:      cfg.Preprocess.WorkingDir,
		OutputDir:       cfg.Preprocess.OutputDir,
		TargetBucket:    cfg.Preprocess.TargetBucket,
		TargetMediaType: cfg.Preprocess.TargetMediaType,
		Concurrency:     cfg.Preprocess.WorkerConcurrency,
		ConvertTimeout:  cfg.Preprocess.ConvertTimeout,
		Backoff:         cfg.Preprocess.Backoff,
		PollInterval:    cfg.Preprocess.PollInterval,
	}

	wrk, err := worker.New(worker.Dependencies{
		Repo:             deps.Repo,
		Downloader:       deps.Downloader,
		Uploader:         deps.Uploader,
		Converter:        deps.Converter,
		WorkspaceManager: deps.WorkspaceManager,
		Callback:         deps.Callback,
	}, workerCfg, logger)
	if err != nil {
		return nil, err
	}

	return &App{
		cfg:        cfg,
		httpServer: transport.NewHTTPServer(cfg.Service.Port, deps.DB, metricsHandler, meter, logger),
		grpcServer: grpcServer,
		worker:     wrk,
		db:         deps.DB,
		telemetry:  deps.Telemetry,
		logger:     logger,
	}, nil
}

func (a *App) Run(ctx context.Context) error {
	fmt.Printf("starting %s on port %d (grpc: %d)\n", a.cfg.Service.Name, a.cfg.Service.Port, a.cfg.Service.GRPCPort)

	errCh := make(chan error, 3)

	go func() {
		errCh <- a.httpServer.Start(ctx)
	}()

	go func() {
		errCh <- a.grpcServer.Start(ctx)
	}()

	go func() {
		errCh <- a.worker.Run(ctx)
	}()

	for i := 0; i < cap(errCh); i++ {
		if err := <-errCh; err != nil && !errors.Is(err, context.Canceled) {
			return err
		}
	}

	return nil
}

func (a *App) Close() {
	if a.db != nil {
		a.db.Close()
	}

	if a.telemetry != nil {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := a.telemetry.Shutdown(shutdownCtx); err != nil {
			a.logger.Error("shutdown telemetry", "error", err)
		}
	}
}
