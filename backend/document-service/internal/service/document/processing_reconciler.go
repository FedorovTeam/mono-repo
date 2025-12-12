package document

import (
	"context"
	"fmt"
	"log"
	"time"

	"backend-document-service/internal/domain"
)

type processingRepo interface {
	RequeueStaleProcessingJobs(ctx context.Context, staleBefore time.Time, limit int, nextRunAt time.Time, reason string) ([]domain.ProcessingJob, error)
}

// ProcessingReconciler возвращает зависшие processing-задачи в pending, чтобы их могли подобрать StageWorker-ы.
type ProcessingReconciler struct {
	repo          processingRepo
	batchSize     int
	stageInterval time.Duration
	gracePeriod   time.Duration
	now           func() time.Time
	metrics       *workerMetrics
	logf          func(format string, args ...interface{})
	retryAttempts int
	retryBackoff  time.Duration
	maxBackoff    time.Duration
	sleep         func(ctx context.Context, d time.Duration)
}

func NewProcessingReconciler(repo processingRepo, batchSize int, stageInterval, grace time.Duration) *ProcessingReconciler {
	if batchSize <= 0 {
		batchSize = 100
	}
	if stageInterval <= 0 {
		stageInterval = time.Minute
	}
	if grace < 0 {
		grace = 0
	}

	return &ProcessingReconciler{
		repo:          repo,
		batchSize:     batchSize,
		stageInterval: stageInterval,
		gracePeriod:   grace,
		now:           time.Now,
		metrics:       newWorkerMetrics("processing_reconciler"),
		logf:          log.Printf,
		retryAttempts: 3,
		retryBackoff:  time.Second,
		maxBackoff:    time.Minute,
		sleep:         sleepWithContext,
	}
}

func (r *ProcessingReconciler) Run(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = time.Minute
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	r.reconcile(ctx)

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			r.reconcile(ctx)
		}
	}
}

func (r *ProcessingReconciler) reconcile(ctx context.Context) {
	cutoff := r.now().Add(-(r.stageInterval + r.gracePeriod))
	nextRun := r.now()
	reason := fmt.Sprintf("restored after exceeding %s in processing", r.stageInterval+r.gracePeriod)

	for {
		jobs, err := r.retryRequeue(ctx, cutoff, nextRun, reason)
		if err != nil {
			return
		}
		r.metrics.observeBatch(ctx, len(jobs))
		if len(jobs) == 0 {
			r.metrics.observeBatch(ctx, 0)
			return
		}

		for _, job := range jobs {
			r.logf("processing-reconciler: job %s (%s) returned to pending after timeout", job.TaskID, job.Stage)
		}

		if len(jobs) < r.batchSize {
			return
		}
	}
}

func (r *ProcessingReconciler) retryRequeue(ctx context.Context, cutoff time.Time, nextRun time.Time, reason string) ([]domain.ProcessingJob, error) {
	maxRetries := r.retryAttempts
	if maxRetries <= 0 {
		maxRetries = 3
	}

	backoff := r.retryBackoff
	if backoff <= 0 {
		backoff = time.Second
	}

	maxBackoff := r.maxBackoff
	if maxBackoff <= 0 {
		maxBackoff = time.Minute
	}

	for attempt := 0; ; attempt++ {
		jobs, err := r.repo.RequeueStaleProcessingJobs(ctx, cutoff, r.batchSize, nextRun, reason)
		if err == nil {
			if attempt > 0 {
				r.metrics.recordRetries(ctx, attempt)
			}
			return jobs, nil
		}

		if ctx.Err() != nil {
			return nil, ctx.Err()
		}

		if attempt >= maxRetries {
			r.logf("processing-reconciler: requeue stale jobs failed after %d retries: %v", attempt, err)
			r.metrics.recordError(ctx)
			r.metrics.recordFinalError(ctx)
			return nil, err
		}

		delay := backoff
		if delay > maxBackoff {
			delay = maxBackoff
		}

		r.logf("processing-reconciler: requeue stale jobs attempt %d failed: %v; retrying in %s", attempt+1, err, delay)
		r.metrics.recordRetries(ctx, 1)
		r.sleep(ctx, delay)
		if backoff < maxBackoff {
			backoff *= 2
			if backoff > maxBackoff {
				backoff = maxBackoff
			}
		}
	}
}

func sleepWithContext(ctx context.Context, d time.Duration) {
	timer := time.NewTimer(d)
	defer timer.Stop()

	select {
	case <-ctx.Done():
	case <-timer.C:
	}
}
