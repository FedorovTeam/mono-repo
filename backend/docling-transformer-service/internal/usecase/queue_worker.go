package usecase

import (
	"context"
	"sync"
	"time"

	transformations "github.com/mono-repo/docling-transformer-service/internal/domain/transformations"
	"github.com/mono-repo/docling-transformer-service/internal/infrastructure/telemetry"
	repo "github.com/mono-repo/docling-transformer-service/internal/repository"
	"github.com/mono-repo/docling-transformer-service/internal/usecase/metrics"
	"go.uber.org/zap"
)

type TaskProcessor interface {
	ProcessTask(ctx context.Context, task *transformations.TransformationTask) (TaskState, error)
}

type QueueWorkerConfig struct {
	Workers      int
	PollInterval time.Duration
	BatchSize    int
	RetryBackoff time.Duration
}

type QueueWorker struct {
	repo    repo.QueueRepository
	metrics *metrics.WorkerMetrics
	logger  *zap.Logger
	cfg     QueueWorkerConfig
	handler TaskProcessor
}

func NewQueueWorker(repository repo.QueueRepository, handler TaskProcessor, metrics *metrics.WorkerMetrics, logger *zap.Logger, cfg QueueWorkerConfig) *QueueWorker {
	if logger == nil {
		logger = zap.NewNop()
	}
	if cfg.Workers <= 0 {
		cfg.Workers = 1
	}
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = time.Second
	}
	if cfg.BatchSize <= 0 {
		cfg.BatchSize = 1
	}
	if cfg.RetryBackoff <= 0 {
		cfg.RetryBackoff = cfg.PollInterval
	}

	return &QueueWorker{repo: repository, handler: handler, metrics: metrics, logger: logger, cfg: cfg}
}

func (w *QueueWorker) Start(ctx context.Context, wg *sync.WaitGroup) {
	for i := 0; i < w.cfg.Workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			w.loop(ctx)
		}()
	}
}

func (w *QueueWorker) loop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		tasks, err := w.repo.AcquireBatch(ctx, w.cfg.BatchSize)
		if err != nil {
			w.logger.Error("acquire next task failed", zap.Error(err))
			w.wait(ctx, w.cfg.RetryBackoff)
			continue
		}

		if len(tasks) == 0 {
			w.wait(ctx, w.cfg.PollInterval)
			continue
		}

		w.metrics.RecordAcquired(ctx, len(tasks))

		for _, task := range tasks {
			w.handleSingle(ctx, task)
		}
	}
}

func (w *QueueWorker) handleSingle(ctx context.Context, task *transformations.TransformationTask) {
	start := time.Now()
	if task.StartedAt != nil {
		start = *task.StartedAt
	}

	taskCtx, correlationID := telemetry.ContextWithCorrelationID(ctx, task.CorrelationID)
	w.logger.Info("processing transformation", zap.String("queue_id", task.ID.String()), zap.String("correlation_id", correlationID))

	state, err := w.handler.ProcessTask(taskCtx, task)
	duration := time.Since(start)

	w.metrics.RecordDuration(taskCtx, float64(duration.Milliseconds()))

	switch {
	case err == nil && state == StateWaitingDocling:
		nextPoll := time.Time{}
		if task.NextPollAt != nil {
			nextPoll = *task.NextPollAt
		}
		w.logger.Info("docling pending", zap.String("queue_id", task.ID.String()), zap.String("correlation_id", correlationID), zap.Time("next_poll_at", nextPoll))
		return
	case err == nil:
		w.metrics.RecordOutcome(taskCtx, true)
		w.logger.Info("transformation completed", zap.String("queue_id", task.ID.String()), zap.String("correlation_id", correlationID), zap.Duration("duration", duration))
	default:
		w.metrics.RecordOutcome(taskCtx, false)
		w.logger.Error("task processing failed", zap.String("queue_id", task.ID.String()), zap.String("correlation_id", correlationID), zap.String("docling_job_id", task.DoclingJobID), zap.Int("polls", task.DoclingPollCount), zap.Error(err))
		if markErr := w.repo.MarkFailed(ctx, task.ID, buildFailureMessage(err, task)); markErr != nil {
			w.logger.Error("mark failed errored", zap.String("queue_id", task.ID.String()), zap.String("correlation_id", correlationID), zap.Error(markErr))
		}
	}
}

func (w *QueueWorker) wait(ctx context.Context, interval time.Duration) {
	select {
	case <-ctx.Done():
	case <-time.After(interval):
	}
}
