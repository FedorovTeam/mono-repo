package worker

import (
	"context"
	"errors"
	"time"

	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"
)

type Config struct {
	MaxWorkers        int
	ProcessingTimeout time.Duration
	CleanupRetention  time.Duration
}

type Processor interface {
	ProcessExtractionJob(ctx context.Context) (bool, error)
}

type Runner struct {
	cfg        Config
	processor  Processor
	logger     *zap.Logger
	pollPeriod time.Duration
}

func NewRunner(cfg Config, processor Processor, logger *zap.Logger) *Runner {
	if logger == nil {
		logger = zap.NewNop()
	}
	pollPeriod := time.Second
	if cfg.ProcessingTimeout > 0 {
		pollPeriod = cfg.ProcessingTimeout / 5
		if pollPeriod < time.Second {
			pollPeriod = time.Second
		}
	}
	return &Runner{
		cfg:        cfg,
		processor:  processor,
		logger:     logger,
		pollPeriod: pollPeriod,
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

	r.logger.Info("starting worker manager",
		zap.Int("max_workers", maxWorkers),
		zap.Duration("poll_period", r.pollPeriod),
		zap.Duration("processing_timeout", r.cfg.ProcessingTimeout),
		zap.Duration("cleanup_retention", r.cfg.CleanupRetention),
	)

	group, groupCtx := errgroup.WithContext(ctx)
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
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		processed, err := r.processor.ProcessExtractionJob(ctx)
		if err != nil {
			r.logger.Error("process extraction job failed", zap.Error(err))
		}
		if !processed {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(r.pollPeriod):
			}
		}
	}
}
