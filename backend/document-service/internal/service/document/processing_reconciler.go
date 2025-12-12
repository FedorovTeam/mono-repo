package document

import (
	"context"
	"fmt"
	"log"
	"time"

	"backend-document-service/internal/model"
)

type processingRepo interface {
	RequeueStaleProcessingJobs(ctx context.Context, staleBefore time.Time, limit int, nextRunAt time.Time, reason string) ([]model.ProcessingJob, error)
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
		jobs, err := r.repo.RequeueStaleProcessingJobs(ctx, cutoff, r.batchSize, nextRun, reason)
		if err != nil {
			r.logf("processing-reconciler: requeue stale jobs: %v", err)
			r.metrics.recordError(ctx)
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
