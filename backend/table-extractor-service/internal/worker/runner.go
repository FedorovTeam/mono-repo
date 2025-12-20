package worker

import (
	"context"
	"time"

	"go.uber.org/zap"
)

type Config struct {
	MaxWorkers        int
	ProcessingTimeout time.Duration
	CleanupRetention  time.Duration
}

type Runner struct {
	cfg    Config
	logger *zap.Logger
}

func NewRunner(cfg Config, logger *zap.Logger) *Runner {
	if logger == nil {
		logger = zap.NewNop()
	}
	return &Runner{
		cfg:    cfg,
		logger: logger,
	}
}

func (r *Runner) Start(ctx context.Context) error {
	r.logger.Info("starting worker manager",
		zap.Int("max_workers", r.cfg.MaxWorkers),
		zap.Duration("processing_timeout", r.cfg.ProcessingTimeout),
		zap.Duration("cleanup_retention", r.cfg.CleanupRetention),
	)

	maintenanceInterval := r.cfg.CleanupRetention
	if maintenanceInterval <= 0 {
		maintenanceInterval = time.Hour
	}

	ticker := time.NewTicker(maintenanceInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			r.logger.Info("stopping worker manager")
			return nil
		case <-ticker.C:
			r.logger.Info("maintenance cycle completed")
		}
	}
}
