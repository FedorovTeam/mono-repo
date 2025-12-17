package usecase

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"

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
}

func NewDoclingPoller(conv domain.Converter, repository repo.QueueRepository, metrics *metrics.WorkerMetrics, logger *zap.Logger, cfg DoclingPollerConfig) *DoclingPoller {
	if logger == nil {
		logger = zap.NewNop()
	}

	return &DoclingPoller{conv: conv, repo: repository, metrics: metrics, logger: logger, cfg: cfg}
}

func (p *DoclingPoller) NextPollTime(now time.Time, pollCount int) time.Time {
	interval := time.Duration(float64(p.cfg.InitialInterval) * math.Pow(p.cfg.BackoffFactor, float64(pollCount)))
	if interval > p.cfg.MaxInterval {
		interval = p.cfg.MaxInterval
	}

	return now.Add(interval)
}

func (p *DoclingPoller) Poll(ctx context.Context, task *transformations.TransformationTask) (*domain.ConvertResult, error) {
	jobID := task.DoclingJobID
	taskID := task.DoclingTaskID
	pollOffset := task.DoclingPollCount

	if p.isWaitingLimitReached(pollOffset) {
		if err := p.requeueDoclingTask(ctx, task, fmt.Sprintf("poll limit reached before status check (%d)", pollOffset)); err != nil {
			return nil, err
		}

		return nil, ErrDoclingWaiting
	}

	pollID := taskID
	if pollID == "" {
		pollID = jobID
	}

	statusResult, err := p.conv.CheckStatus(ctx, pollID)
	newPollCount := pollOffset + 1
	task.DoclingTaskID = pollID
	task.DoclingJobID = jobID
	task.DoclingPollCount = newPollCount

	if err := p.repo.UpdateDoclingInfo(ctx, task.ID, pollID, jobID, newPollCount); err != nil {
		return nil, fmt.Errorf("store docling polling progress: %w", err)
	}

	if err != nil {
		p.metrics.RecordDoclingPolls(ctx, 1)
		if isTemporary, statusCode := isTemporaryDoclingError(err); isTemporary {
			if p.isWaitingLimitReached(newPollCount) {
				if err := p.requeueDoclingTask(ctx, task, fmt.Sprintf("temporary error limit reached (%d)", newPollCount)); err != nil {
					return nil, err
				}

				return nil, ErrDoclingWaiting
			}

			nextPoll := p.NextPollTime(time.Now(), newPollCount)
			task.DoclingPollCount = newPollCount
			task.NextPollAt = &nextPoll
			if markErr := p.repo.MarkDoclingWaiting(ctx, task.ID, pollID, jobID, newPollCount, nextPoll); markErr != nil {
				return nil, fmt.Errorf("store docling waiting: %w", markErr)
			}

			p.metrics.RecordTemporaryRetry(ctx, 1)
			p.logger.Warn("docling temporary error, will retry", zap.String("task_id", pollID), zap.String("job_id", jobID), zap.Int("docling_polls", newPollCount), zap.Int("status_code", statusCode), zap.Time("next_poll_at", nextPoll), zap.Error(err))

			return nil, ErrDoclingWaiting
		}

		p.logger.Error("docling status check failed", zap.String("task_id", pollID), zap.String("job_id", jobID), zap.Int("docling_polls", newPollCount), zap.Error(err))

		if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
			if p.isWaitingLimitReached(newPollCount) {
				if err := p.requeueDoclingTask(ctx, task, fmt.Sprintf("timeout limit reached (%d)", newPollCount)); err != nil {
					return nil, err
				}

				return nil, ErrDoclingWaiting
			}

			nextPoll := p.NextPollTime(time.Now(), newPollCount)
			task.DoclingPollCount = newPollCount
			if markErr := p.repo.MarkDoclingWaiting(ctx, task.ID, pollID, jobID, newPollCount, nextPoll); markErr != nil {
				return nil, fmt.Errorf("store docling waiting: %w", markErr)
			}

			task.NextPollAt = &nextPoll
			p.logger.Info("docling still processing", zap.String("task_id", pollID), zap.String("job_id", jobID), zap.Time("next_poll_at", nextPoll), zap.Int("docling_polls", newPollCount))

			return nil, ErrDoclingWaiting
		}

		return nil, fmt.Errorf("docling status: %w", err)
	}

	p.metrics.RecordDoclingPolls(ctx, 1)
	p.logger.Info("docling status checked", zap.String("task_id", pollID), zap.String("job_id", jobID), zap.String("status", statusResult.Status), zap.Int("docling_polls", newPollCount))

	if statusResult.Result == nil {
		if p.isWaitingLimitReached(newPollCount) {
			if err := p.requeueDoclingTask(ctx, task, fmt.Sprintf("waiting limit reached (%d)", newPollCount)); err != nil {
				return nil, err
			}

			return nil, ErrDoclingWaiting
		}

		nextPoll := p.NextPollTime(time.Now(), newPollCount)
		if err := p.repo.MarkDoclingWaiting(ctx, task.ID, pollID, jobID, newPollCount, nextPoll); err != nil {
			return nil, fmt.Errorf("store docling waiting: %w", err)
		}

		task.NextPollAt = &nextPoll
		p.logger.Info("docling still processing", zap.String("task_id", pollID), zap.String("job_id", jobID), zap.Time("next_poll_at", nextPoll), zap.Int("docling_polls", newPollCount))

		return nil, ErrDoclingWaiting
	}

	p.metrics.RecordDoclingWait(ctx, float64(time.Since(task.CreatedAt).Milliseconds()))

	result := *statusResult.Result
	return &result, nil
}

func isTemporaryDoclingError(err error) (bool, int) {
	var tmp domain.TemporaryError
	if errors.As(err, &tmp) && tmp.Temporary() {
		var code domain.StatusCoder
		if errors.As(err, &code) {
			return true, code.StatusCode()
		}

		return true, 0
	}

	return false, 0
}

func (p *DoclingPoller) isWaitingLimitReached(pollCount int) bool {
	return p.cfg.MaxAttempts > 0 && pollCount >= p.cfg.MaxAttempts
}

func (p *DoclingPoller) requeueDoclingTask(ctx context.Context, task *transformations.TransformationTask, reason string) error {
	taskID := task.DoclingTaskID
	jobID := task.DoclingJobID

	var nextPollAt *time.Time
	if p.shouldDelayRestart(task) && p.cfg.RequeueDelay > 0 {
		delayed := time.Now().Add(p.cfg.RequeueDelay)
		nextPollAt = &delayed
	}

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

	logger := p.logger.With(
		zap.String("queue_id", task.ID.String()),
		zap.String("docling_task_id", taskID),
		zap.String("docling_job_id", jobID),
		zap.String("reason", reason),
	)

	if nextPollAt != nil {
		logger = logger.With(zap.Time("next_poll_at", *nextPollAt))
	}

	logger.Warn("docling job requeued")

	return nil
}

func (p *DoclingPoller) shouldDelayRestart(task *transformations.TransformationTask) bool {
	return p.cfg.MaxRestarts > 0 && task.RetryCount >= p.cfg.MaxRestarts
}
