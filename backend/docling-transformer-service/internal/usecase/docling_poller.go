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

	if cfg.InitialInterval <= 0 {
		cfg.InitialInterval = 15 * time.Second
	}
	if cfg.BackoffFactor <= 1 {
		cfg.BackoffFactor = 2
	}
	if cfg.MaxInterval <= 0 {
		cfg.MaxInterval = 30 * time.Minute
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
	pollOffset := task.DoclingPollCount

	if p.cfg.MaxAttempts > 0 && pollOffset >= p.cfg.MaxAttempts {
		return nil, fmt.Errorf("%w: job %s has no remaining attempts after %d polls", transformations.ErrPollingAttemptsExceeded, jobID, pollOffset)
	}

	statusResult, err := p.conv.CheckStatus(ctx, jobID)
	newPollCount := pollOffset + 1
	task.DoclingJobID = jobID
	task.DoclingPollCount = newPollCount

	if err := p.repo.UpdateDoclingInfo(ctx, task.ID, jobID, newPollCount); err != nil {
		return nil, fmt.Errorf("store docling polling progress: %w", err)
	}

	if err != nil {
		p.metrics.RecordDoclingPolls(ctx, 1)
		if isTemporary, statusCode := isTemporaryDoclingError(err); isTemporary {
			nextPoll := p.NextPollTime(time.Now(), newPollCount)
			task.DoclingPollCount = newPollCount
			task.NextPollAt = &nextPoll
			if markErr := p.repo.MarkDoclingWaiting(ctx, task.ID, jobID, newPollCount, nextPoll); markErr != nil {
				return nil, fmt.Errorf("store docling waiting: %w", markErr)
			}

			p.metrics.RecordTemporaryRetry(ctx, 1)
			p.logger.Warn("docling temporary error, will retry", zap.String("job_id", jobID), zap.Int("docling_polls", newPollCount), zap.Int("status_code", statusCode), zap.Time("next_poll_at", nextPoll), zap.Error(err))

			return nil, ErrDoclingWaiting
		}

		p.logger.Error("docling status check failed", zap.String("job_id", jobID), zap.Int("docling_polls", newPollCount), zap.Error(err))

		if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
			nextPoll := p.NextPollTime(time.Now(), newPollCount)
			task.DoclingPollCount = newPollCount
			if markErr := p.repo.MarkDoclingWaiting(ctx, task.ID, jobID, newPollCount, nextPoll); markErr != nil {
				return nil, fmt.Errorf("store docling waiting: %w", markErr)
			}

			task.NextPollAt = &nextPoll
			p.logger.Info("docling still processing", zap.String("job_id", jobID), zap.Time("next_poll_at", nextPoll), zap.Int("docling_polls", newPollCount))

			return nil, ErrDoclingWaiting
		}

		return nil, fmt.Errorf("docling status: %w", err)
	}

	p.metrics.RecordDoclingPolls(ctx, 1)
	p.logger.Info("docling status checked", zap.String("job_id", jobID), zap.String("status", statusResult.Status), zap.Int("docling_polls", newPollCount))

	if statusResult.Result == nil {
		if p.cfg.MaxAttempts > 0 && newPollCount >= p.cfg.MaxAttempts {
			return nil, fmt.Errorf("%w: job %s exceeded maximum polls (%d)", transformations.ErrPollingAttemptsExceeded, jobID, newPollCount)
		}

		nextPoll := p.NextPollTime(time.Now(), newPollCount)
		if err := p.repo.MarkDoclingWaiting(ctx, task.ID, jobID, newPollCount, nextPoll); err != nil {
			return nil, fmt.Errorf("store docling waiting: %w", err)
		}

		task.NextPollAt = &nextPoll
		p.logger.Info("docling still processing", zap.String("job_id", jobID), zap.Time("next_poll_at", nextPoll), zap.Int("docling_polls", newPollCount))

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
