package worker

import (
	"context"
	"errors"
	"time"

	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"
)

type Config struct {
	BatchSize           int
	MaxWorkers          int
	ProcessingTimeout   time.Duration
	CleanupRetention    time.Duration
	MaintenanceInterval time.Duration
	Retry               RetryConfig
}

type Processor interface {
	ProcessExtractionJob(ctx context.Context) (bool, error)
	RestoreStuckProcessing(ctx context.Context, processingTimeout time.Duration, maxAttempts int, backoffInterval, maxBackoff time.Duration) (int, int, error)
	RetryFailed(ctx context.Context, maxAttempts int, retryDelay time.Duration) (int, error)
	CleanupFinished(ctx context.Context, retention time.Duration) (int, error)
}

type RetryConfig struct {
	MaxAttempts     int
	BackoffInterval time.Duration
	MaxBackoff      time.Duration
}

type Runner struct {
	cfg               Config
	processor         Processor
	logger            *zap.Logger
	pollPeriod        time.Duration
	maintenancePeriod time.Duration
}

func NewRunner(cfg Config, processor Processor, logger *zap.Logger) *Runner {
	if logger == nil {
		logger = zap.NewNop()
	}
	pollPeriod := cfg.Retry.BackoffInterval
	if pollPeriod <= 0 {
		pollPeriod = time.Second
	}
	maintenancePeriod := cfg.MaintenanceInterval
	if maintenancePeriod <= 0 {
		maintenancePeriod = pollPeriod
	}
	return &Runner{
		cfg:               cfg,
		processor:         processor,
		logger:            logger,
		pollPeriod:        pollPeriod,
		maintenancePeriod: maintenancePeriod,
	}
}

func (r *Runner) Start(ctx context.Context) error {
	if r.processor == nil {
		r.logger.Warn("worker processor is not configured")
		<-ctx.Done()
		return ctx.Err()
	}

	maxWorkers := r.cfg.MaxWorkers
	if maxWorkers <= 0 {
		maxWorkers = 1
	}

	r.restoreProcessing(ctx)

	r.logger.Info("starting worker manager",
		zap.Int("batch_size", r.cfg.BatchSize),
		zap.Int("max_workers", maxWorkers),
		zap.Duration("poll_period", r.pollPeriod),
		zap.Duration("processing_timeout", r.cfg.ProcessingTimeout),
		zap.Duration("cleanup_retention", r.cfg.CleanupRetention),
		zap.Int("retry_max_attempts", r.cfg.Retry.MaxAttempts),
		zap.Duration("retry_backoff", r.cfg.Retry.BackoffInterval),
	)

	group, groupCtx := errgroup.WithContext(ctx)
	group.Go(func() error {
		return r.maintenanceLoop(groupCtx)
	})
	for i := 0; i < maxWorkers; i++ {
		group.Go(func() error {
			return r.workerLoop(groupCtx)
		})
	}

	err := group.Wait()
	if errors.Is(err, context.Canceled) {
		return nil
	}
	return err
}

func (r *Runner) workerLoop(ctx context.Context) error {
	delay := newBackoff(r.cfg.Retry.BackoffInterval, r.cfg.Retry.MaxBackoff, r.pollPeriod)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}

		r.logger.Debug("worker tick started",
			zap.Duration("poll_period", r.pollPeriod),
		)

		processCtx := ctx
		cancel := func() {}
		if r.cfg.ProcessingTimeout > 0 {
			processCtx, cancel = context.WithTimeout(ctx, r.cfg.ProcessingTimeout)
		}

		processed, err := r.processor.ProcessExtractionJob(processCtx)
		cancel()
		if err != nil {
			r.logger.Error("process extraction job failed", zap.Error(err))
		}
		if !processed {
			r.logger.Debug("no jobs processed on tick",
				zap.Duration("next_delay", delay.cur),
			)
			if err := r.wait(ctx, delay.next()); err != nil {
				return err
			}
			continue
		}
		r.logger.Debug("job processed on tick")
		delay.reset()
	}
}

func (r *Runner) maintenanceLoop(ctx context.Context) error {
	ticker := time.NewTicker(r.maintenancePeriod)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}

		restored, failed, err := r.processor.RestoreStuckProcessing(ctx, r.cfg.ProcessingTimeout, r.cfg.Retry.MaxAttempts, r.cfg.Retry.BackoffInterval, r.cfg.Retry.MaxBackoff)
		if err != nil {
			r.logger.Warn("failed to restore stuck processing jobs", zap.Error(err))
		} else if restored > 0 || failed > 0 {
			r.logger.Info("restored stuck jobs", zap.Int("restored", restored), zap.Int("marked_failed", failed))
		} else {
			r.logger.Debug("no stuck jobs to restore",
				zap.Duration("processing_timeout", r.cfg.ProcessingTimeout),
			)
		}

		retried, err := r.processor.RetryFailed(ctx, r.cfg.Retry.MaxAttempts, r.cfg.Retry.BackoffInterval)
		if err != nil {
			r.logger.Warn("failed to retry jobs", zap.Error(err))
		} else if retried > 0 {
			r.logger.Info("retried failed jobs", zap.Int("count", retried))
		} else {
			r.logger.Debug("no failed jobs to retry",
				zap.Int("max_attempts", r.cfg.Retry.MaxAttempts),
			)
		}

		cleaned, err := r.processor.CleanupFinished(ctx, r.cfg.CleanupRetention)
		if err != nil {
			r.logger.Warn("failed to cleanup finished jobs", zap.Error(err))
		} else if cleaned > 0 {
			r.logger.Info("cleaned finished jobs", zap.Int("removed", cleaned))
		} else {
			r.logger.Debug("no finished jobs to cleanup",
				zap.Duration("cleanup_retention", r.cfg.CleanupRetention),
			)
		}
	}
}

func (r *Runner) restoreProcessing(ctx context.Context) {
	if r.processor == nil {
		return
	}
	restored, failed, err := r.processor.RestoreStuckProcessing(ctx, 0, r.cfg.Retry.MaxAttempts, 0, r.cfg.Retry.MaxBackoff)
	if err != nil {
		r.logger.Warn("failed to restore processing jobs on startup", zap.Error(err))
		return
	}
	if restored > 0 || failed > 0 {
		r.logger.Info("restored processing jobs on startup", zap.Int("restored", restored), zap.Int("marked_failed", failed))
	}
}

func (r *Runner) wait(ctx context.Context, duration time.Duration) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(duration):
		return nil
	}
}

type backoff struct {
	base   time.Duration
	max    time.Duration
	min    time.Duration
	cur    time.Duration
	inited bool
}

func newBackoff(base, max, min time.Duration) *backoff {
	if base <= 0 {
		base = min
	}
	if base <= 0 {
		base = time.Second
	}
	if min <= 0 {
		min = base
	}
	return &backoff{base: base, max: max, min: min, cur: base}
}

func (b *backoff) next() time.Duration {
	if !b.inited {
		b.inited = true
		return b.cur
	}
	next := b.cur + b.base
	if next < b.min {
		next = b.min
	}
	if b.max > 0 && next > b.max {
		next = b.max
	}
	b.cur = next
	return b.cur
}

func (b *backoff) reset() {
	b.cur = b.base
	b.inited = false
}
