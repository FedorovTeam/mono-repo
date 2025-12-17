// backend/docling-transformer-service/internal/usecase/maintenance_runner.go
package usecase

import (
	"context"
	"sync"
	"time"

	repo "github.com/mono-repo/docling-transformer-service/internal/repository"
	"github.com/mono-repo/docling-transformer-service/internal/usecase/metrics"
	"go.uber.org/zap"
)

type MaintenanceConfig struct {
	Interval             time.Duration
	ProcessingTimeout    time.Duration
	MaxProcessingRetries int
	CleanupRetention     time.Duration
	DoclingMaxLifetime   time.Duration
	DoclingMaxAttempts   int
	FailedRetryDelay     time.Duration
	MaxFailedRetries     int
}

type MaintenanceRunner struct {
	repo    repo.QueueRepository
	metrics *metrics.WorkerMetrics
	logger  *zap.Logger
	cfg     MaintenanceConfig
}

func NewMaintenanceRunner(repository repo.QueueRepository, metrics *metrics.WorkerMetrics, logger *zap.Logger, cfg MaintenanceConfig) *MaintenanceRunner {
	if logger == nil {
		logger = zap.NewNop()
	}

	return &MaintenanceRunner{repo: repository, metrics: metrics, logger: logger, cfg: cfg}
}

func (r *MaintenanceRunner) Start(ctx context.Context, wg *sync.WaitGroup) {
	wg.Add(1)
	go func() {
		defer wg.Done()
		ticker := time.NewTicker(r.cfg.Interval)
		defer ticker.Stop()

		r.runOnce(ctx)

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				r.runOnce(ctx)
			}
		}
	}()
}

func (r *MaintenanceRunner) runOnce(ctx context.Context) {
	if pending, processing, doclingWaiting, err := r.repo.CountByStatus(ctx); err != nil {
		r.logger.Error("count tasks by status failed", zap.Error(err))
	} else {
		r.metrics.RecordQueueSizes(int64(pending), int64(processing), int64(doclingWaiting))
		r.logger.Info("queue status snapshot", zap.Int("pending", pending), zap.Int("processing", processing), zap.Int("docling_waiting", doclingWaiting))
	}

	if ready, expired, err := r.repo.ActivateWaiting(ctx, time.Now(), r.cfg.DoclingMaxLifetime); err != nil {
		r.logger.Error("activate waiting tasks failed", zap.Error(err))
	} else if ready > 0 || expired > 0 {
		r.logger.Info("waiting tasks processed", zap.Int("activated", ready), zap.Int("expired", expired))
	}

	if r.cfg.ProcessingTimeout > 0 {
		restored, failed, err := r.repo.RestoreStuckProcessing(ctx, r.cfg.ProcessingTimeout, r.cfg.MaxProcessingRetries, r.cfg.DoclingMaxAttempts)
		if err != nil {
			r.logger.Error("restore stuck tasks failed", zap.Error(err))
		} else if restored > 0 || failed > 0 {
			r.logger.Warn("stuck tasks processed", zap.Int("restored", restored), zap.Int("failed", failed), zap.Duration("processing_timeout", r.cfg.ProcessingTimeout))
		}

		r.metrics.RecordRecoveries(ctx, restored, failed)
	}

	if r.cfg.FailedRetryDelay > 0 && r.cfg.MaxFailedRetries > 0 {
		retried, exhausted, err := r.repo.RetryFailed(ctx, time.Now().Add(-r.cfg.FailedRetryDelay), r.cfg.MaxFailedRetries)
		if err != nil {
			r.logger.Error("retry failed tasks failed", zap.Error(err))
		} else if retried > 0 || exhausted > 0 {
			r.logger.Info("failed tasks processed", zap.Int("restarted", retried), zap.Int("exhausted", exhausted), zap.Duration("retry_delay", r.cfg.FailedRetryDelay), zap.Int("max_retries", r.cfg.MaxFailedRetries))
		}

		r.metrics.RecordFailedRetries(ctx, retried, exhausted)
	}

	if r.cfg.CleanupRetention > 0 {
		removed, err := r.repo.CleanupFinished(ctx, r.cfg.CleanupRetention)
		if err != nil {
			r.logger.Error("cleanup finished tasks failed", zap.Error(err))
		} else if removed > 0 {
			r.logger.Info("cleanup finished tasks completed", zap.Int("removed", removed), zap.Duration("retention", r.cfg.CleanupRetention))
		}
	}
}
