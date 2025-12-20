// backend/docling-transformer-service/internal/usecase/docling_poller.go
package usecase

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/mono-repo/docling-transformer-service/internal/domain"
	transformations "github.com/mono-repo/docling-transformer-service/internal/domain/transformations"
	repo "github.com/mono-repo/docling-transformer-service/internal/repository"
	"github.com/mono-repo/docling-transformer-service/internal/usecase/metrics"
	"go.uber.org/zap"
)

var ErrDoclingWaiting = errors.New("waiting for docling")

type DoclingPollerConfig struct {
	InitialInterval time.Duration
	MaxInterval     time.Duration
	BackoffFactor   float64
	MaxAttempts     int
	RequeueDelay    time.Duration
	MaxRestarts     int
}

type DoclingPoller struct {
	conv    domain.Converter
	repo    repo.QueueRepository
	metrics *metrics.WorkerMetrics
	logger  *zap.Logger
	cfg     DoclingPollerConfig
	errors  *pollErrorTracker
}

func NewDoclingPoller(
	conv domain.Converter,
	repository repo.QueueRepository,
	metrics *metrics.WorkerMetrics,
	logger *zap.Logger,
	cfg DoclingPollerConfig,
) *DoclingPoller {
	if logger == nil {
		logger = zap.NewNop()
	}
	return &DoclingPoller{
		conv:    conv,
		repo:    repository,
		metrics: metrics,
		logger:  logger,
		cfg:     cfg,
		errors:  newPollErrorTracker(),
	}
}

func (p *DoclingPoller) NextPollTime(now time.Time, pollCount int) time.Time {
	interval := time.Duration(float64(p.cfg.InitialInterval) * math.Pow(p.cfg.BackoffFactor, float64(pollCount)))
	if interval > p.cfg.MaxInterval {
		interval = p.cfg.MaxInterval
	}
	return now.Add(interval)
}

func (p *DoclingPoller) Poll(ctx context.Context, task *transformations.TransformationTask) (*domain.ConvertResult, error) {
	taskID := task.DoclingTaskID
	if taskID == "" {
		taskID = task.DoclingJobID
	}
	ids := domain.DoclingIdentifiers{TaskID: taskID, JobID: task.DoclingJobID}
	pollOffset := task.DoclingPollCount

	if taskID == "" {
		return nil, fmt.Errorf("docling task identifier is empty")
	}

	if p.isRestartLimitReached(task) {
		return nil, p.buildRestartLimitError(task)
	}

	if p.isWaitingLimitReached(pollOffset) {
		p.errors.Clear(task.ID)
		if err := p.requeueDoclingTask(ctx, task, fmt.Sprintf("poll limit reached before status check (%d)", pollOffset)); err != nil {
			return nil, err
		}
		return nil, ErrDoclingWaiting
	}

	statusResult, checkErr := p.conv.CheckStatus(ctx, taskID)

	newPollCount := pollOffset + 1
	task.DoclingTaskID = ids.TaskID
	task.DoclingJobID = ids.JobID
	task.DoclingPollCount = newPollCount

	logIDs := []zap.Field{
		zap.String("docling_task_id", ids.TaskID),
		zap.Int("docling_polls", newPollCount),
	}
	if ids.JobID != "" {
		logIDs = append(logIDs, zap.String("docling_job_id", ids.JobID))
	}

	// прогресс поллинга сохраняем всегда
	if err := p.repo.UpdateDoclingInfo(ctx, task.ID, ids.TaskID, ids.JobID, newPollCount); err != nil {
		return nil, fmt.Errorf("store docling polling progress: %w", err)
	}

	p.metrics.RecordDoclingPolls(ctx, 1)

	if checkErr != nil {
		var doclingFailure *domain.DoclingFailureError
		if errors.As(checkErr, &doclingFailure) {
			return nil, checkErr
		}

		streak, signature := p.errors.Add(task.ID, checkErr)
		if streak >= 2 {
			if err := p.requeueDoclingTask(ctx, task, fmt.Sprintf("repeated docling status error (%s)", signature)); err != nil {
				return nil, err
			}
			p.errors.Clear(task.ID)
			return nil, ErrDoclingWaiting
		}

		// Любая ошибка статуса → переводим обратно в waiting_docling с backoff.
		if p.isWaitingLimitReached(newPollCount) {
			if err := p.requeueDoclingTask(ctx, task, fmt.Sprintf("status check error limit reached (%d): %v", newPollCount, checkErr)); err != nil {
				return nil, err
			}
			p.errors.Clear(task.ID)
			return nil, ErrDoclingWaiting
		}

		nextPoll := p.NextPollTime(time.Now(), newPollCount)
		if err := p.repo.MarkDoclingWaiting(ctx, task.ID, ids.TaskID, ids.JobID, newPollCount, nextPoll); err != nil {
			return nil, fmt.Errorf("store docling waiting: %w", err)
		}
		task.NextPollAt = &nextPoll

		p.logger.Warn("docling status check error, will retry", append(logIDs, zap.Time("next_poll_at", nextPoll), zap.Error(checkErr))...)
		return nil, ErrDoclingWaiting
	}

	p.errors.Clear(task.ID)

	if statusResult.Result == nil {
		if p.isWaitingLimitReached(newPollCount) {
			if err := p.requeueDoclingTask(ctx, task, fmt.Sprintf("waiting limit reached (%d)", newPollCount)); err != nil {
				return nil, err
			}
			return nil, ErrDoclingWaiting
		}

		nextPoll := p.NextPollTime(time.Now(), newPollCount)
		if err := p.repo.MarkDoclingWaiting(ctx, task.ID, ids.TaskID, ids.JobID, newPollCount, nextPoll); err != nil {
			return nil, fmt.Errorf("store docling waiting: %w", err)
		}
		task.NextPollAt = &nextPoll

		p.logger.Info("docling still processing", append(logIDs, zap.Time("next_poll_at", nextPoll))...)
		return nil, ErrDoclingWaiting
	}

	p.metrics.RecordDoclingWait(ctx, float64(time.Since(task.CreatedAt).Milliseconds()))
	result := *statusResult.Result
	return &result, nil
}

func (p *DoclingPoller) isWaitingLimitReached(pollCount int) bool {
	return p.cfg.MaxAttempts > 0 && pollCount >= p.cfg.MaxAttempts
}

func (p *DoclingPoller) requeueDoclingTask(ctx context.Context, task *transformations.TransformationTask, reason string) error {
	if p.isRestartLimitReached(task) {
		return p.buildRestartLimitError(task)
	}

	var nextPollAt *time.Time
	if p.cfg.RequeueDelay > 0 {
		t := time.Now().Add(p.cfg.RequeueDelay)
		nextPollAt = &t
	}

	p.errors.Clear(task.ID)

	if err := p.repo.RequeueDoclingWaiting(ctx, task.ID, nextPollAt); err != nil {
		return fmt.Errorf("requeue docling waiting: %w", err)
	}

	task.DoclingTaskID = ""
	task.DoclingJobID = ""
	task.DoclingPollCount = 0
	task.NextPollAt = nextPollAt
	task.RetryCount++

	if p.metrics != nil {
		p.metrics.RecordDoclingRequeued(ctx)
	}

	p.logger.Warn("docling job requeued",
		zap.String("queue_id", task.ID.String()),
		zap.String("reason", reason),
	)

	return nil
}

func (p *DoclingPoller) doclingRestarts(task *transformations.TransformationTask) int {
	if task == nil {
		return 0
	}

	retryCount := task.RetryCount
	if retryCount <= 1 {
		return 0
	}

	restarts := retryCount - 1
	if restarts < 0 {
		restarts = 0
	}

	return restarts / 2
}

func (p *DoclingPoller) isRestartLimitReached(task *transformations.TransformationTask) bool {
	if p.cfg.MaxRestarts <= 0 {
		return false
	}

	return p.doclingRestarts(task) >= p.cfg.MaxRestarts
}

func (p *DoclingPoller) buildRestartLimitError(task *transformations.TransformationTask) error {
	restarts := p.doclingRestarts(task)
	err := &transformations.DoclingRestartsExceededError{
		Restarts:    restarts,
		MaxRestarts: p.cfg.MaxRestarts,
	}

	fields := []zap.Field{
		zap.String("queue_id", task.ID.String()),
		zap.Int("restarts", restarts),
		zap.Int("max_restarts", p.cfg.MaxRestarts),
	}

	if task.DoclingTaskID != "" {
		fields = append(fields, zap.String("docling_task_id", task.DoclingTaskID))
	}
	if task.DoclingJobID != "" {
		fields = append(fields, zap.String("docling_job_id", task.DoclingJobID))
	}

	p.logger.Error("docling restarts limit reached", fields...)

	return err
}

type pollErrorTracker struct {
	mu   sync.Mutex
	last map[uuid.UUID]errorSignature
}

type errorSignature struct {
	value string
	count int
}

func newPollErrorTracker() *pollErrorTracker {
	return &pollErrorTracker{last: make(map[uuid.UUID]errorSignature)}
}

func (t *pollErrorTracker) Add(taskID uuid.UUID, err error) (int, string) {
	signature := describeError(err)

	t.mu.Lock()
	defer t.mu.Unlock()

	info := t.last[taskID]
	if info.value == signature {
		info.count++
	} else {
		info.value = signature
		info.count = 1
	}

	t.last[taskID] = info

	return info.count, info.value
}

func (t *pollErrorTracker) Clear(taskID uuid.UUID) {
	t.mu.Lock()
	defer t.mu.Unlock()

	delete(t.last, taskID)
}

func describeError(err error) string {
	var status domain.StatusCoder
	if errors.As(err, &status) {
		return fmt.Sprintf("status:%d", status.StatusCode())
	}

	return fmt.Sprintf("error:%T:%s", err, err.Error())
}
