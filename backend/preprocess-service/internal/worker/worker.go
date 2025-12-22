package worker

import (
	"context"
	"fmt"
	"runtime/debug"
	"strconv"
	"sync"
	"time"

	"go.uber.org/zap"
	"golang.org/x/sync/semaphore"

	"preprocess-service/internal/domain"
	"preprocess-service/internal/usecase/ports"
	"preprocess-service/internal/usecase/preprocess"
)

// Config описывает настройки воркера предобработки.
type Config struct {
	LibreOfficePath string
	WorkingDir      string
	TargetBucket    string
	TargetMediaType string
	OutputDir       string
	Concurrency     int64 // принудительно приводится к 1, параллелизм отключён
	ProcessTimeout  time.Duration
	ConvertTimeout  time.Duration
	Backoff         time.Duration
	PollInterval    time.Duration
}

type Worker struct {
	cfg       Config
	sem       *semaphore.Weighted
	logger    *zap.Logger
	waitGroup sync.WaitGroup
	processor *preprocess.Service
	repo      domain.PreprocessJobRepository
}

type Dependencies struct {
	Repo             domain.PreprocessJobRepository
	Downloader       ports.StorageDownloader
	Uploader         ports.StorageUploader
	Converter        ports.DocumentConverter
	WorkspaceManager ports.WorkspaceManager
	Callback         ports.CallbackSender
}

func New(deps Dependencies, cfg Config, logger *zap.Logger) (*Worker, error) {
	if deps.Repo == nil {
		return nil, fmt.Errorf("queue repository is required")
	}
	if deps.Downloader == nil {
		return nil, fmt.Errorf("storage downloader is required")
	}
	if deps.Uploader == nil {
		return nil, fmt.Errorf("storage uploader is required")
	}
	if deps.Converter == nil {
		return nil, fmt.Errorf("document converter is required")
	}
	if deps.WorkspaceManager == nil {
		return nil, fmt.Errorf("workspace manager is required")
	}
	if deps.Callback == nil {
		return nil, fmt.Errorf("callback sender is required")
	}

	if logger == nil {
		logger = zap.NewNop()
	}

	normalizedCfg := normalizeConfig(cfg)

	serviceCfg := preprocess.Config{
		LibreOfficePath: normalizedCfg.LibreOfficePath,
		WorkingDir:      normalizedCfg.WorkingDir,
		OutputDir:       normalizedCfg.OutputDir,
		TargetBucket:    normalizedCfg.TargetBucket,
		TargetMediaType: normalizedCfg.TargetMediaType,
		ProcessTimeout:  normalizedCfg.ProcessTimeout,
		ConvertTimeout:  normalizedCfg.ConvertTimeout,
		Backoff:         normalizedCfg.Backoff,
	}

	processor, err := preprocess.NewService(deps.Repo, deps.Downloader, deps.Uploader, deps.Converter, deps.WorkspaceManager, deps.Callback, serviceCfg, logger)
	if err != nil {
		return nil, err
	}

	return &Worker{
		cfg:       normalizedCfg,
		sem:       semaphore.NewWeighted(normalizedCfg.Concurrency),
		logger:    logger,
		processor: processor,
		repo:      deps.Repo,
	}, nil
}

func normalizeConfig(cfg Config) Config {
	cfg.Concurrency = 1
	if cfg.ConvertTimeout <= 0 {
		cfg.ConvertTimeout = 2 * time.Minute
	}
	if cfg.ProcessTimeout <= 0 {
		cfg.ProcessTimeout = cfg.ConvertTimeout + time.Minute
	}
	if cfg.Backoff <= 0 {
		cfg.Backoff = 30 * time.Second
	}
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = 2 * time.Second
	}
	return cfg
}

func (w *Worker) Run(ctx context.Context) error {
	w.ensureLogger()

	ticker := time.NewTicker(w.cfg.PollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			w.waitGroup.Wait()
			return ctx.Err()
		default:
		}

		w.logger.Debug("requeuing stuck jobs",
			zap.Duration("convert_timeout", w.cfg.ConvertTimeout),
			zap.Duration("backoff", w.cfg.Backoff),
		)
		if err := w.repo.RequeueStuck(ctx, w.cfg.ConvertTimeout, w.cfg.Backoff); err != nil {
			w.logger.Error("failed to requeue stuck jobs", zap.Error(err))
			if !w.waitForNext(ctx, ticker) {
				return ctx.Err()
			}
			continue
		}

		w.logger.Debug("picking next job for processing")
		job, found, err := w.repo.NextForProcessing(ctx)
		if err != nil {
			w.logger.Error("failed to pick job", zap.Error(err))
			if !w.waitForNext(ctx, ticker) {
				return ctx.Err()
			}
			continue
		}

		if !found {
			w.logger.Debug("no jobs ready for processing, waiting for next tick")
			if !w.waitForNext(ctx, ticker) {
				return ctx.Err()
			}
			continue
		}

		w.logger.Debug("job picked for processing",
			zap.String("job_id", job.ID),
			zap.String("correlation_id", job.CorrelationID),
		)
		if err := w.sem.Acquire(ctx, 1); err != nil {
			return err
		}

		w.waitGroup.Add(1)
		go func(job domain.PreprocessJob) {
			defer w.waitGroup.Done()
			defer w.sem.Release(1)
			w.processJob(ctx, job)
		}(job)
	}
}

func (w *Worker) waitForNext(ctx context.Context, ticker *time.Ticker) bool {
	select {
	case <-ctx.Done():
		w.waitGroup.Wait()
		return false
	case <-ticker.C:
		return true
	}
}

func (w *Worker) processJob(ctx context.Context, job domain.PreprocessJob) {
	w.ensureLogger()
	logger := w.logger.With(
		zap.String("job_id", job.ID),
		zap.String("correlation_id", job.CorrelationID),
	)
	logger.Info("start processing job")

	defer func() {
		if recovered := recover(); recovered != nil {
			logger.Error("job panicked", zap.Any("panic", recovered))
			w.handleJobPanic(ctx, job, recovered)
		}
	}()

	if w.processor == nil {
		logger.Error("processor is not configured")
		return
	}

	if err := w.processor.Process(ctx, preprocess.Job(job)); err != nil {
		logger.Error("job processing finished with error", zap.Error(err))
	}
}

func (w *Worker) ensureLogger() {
	if w.logger == nil {
		w.logger = zap.NewNop()
	}
}

func (w *Worker) handleJobPanic(ctx context.Context, job domain.PreprocessJob, recovered any) {
	w.ensureLogger()
	logger := w.logger.With(
		zap.String("job_id", job.ID),
		zap.String("correlation_id", job.CorrelationID),
	)

	failure := domain.Failure{
		Code:      domain.ErrorCodeProcessingError,
		Message:   fmt.Sprintf("panic: %v", recovered),
		Permanent: false,
		Details: map[string]string{
			"attempt": strconv.Itoa(job.Attempts),
			"panic":   fmt.Sprint(recovered),
			"stack":   string(debug.Stack()),
		},
	}
	failure.Details["code"] = failure.Code.String()
	failure.Details["permanent"] = strconv.FormatBool(failure.Permanent)

	policy := domain.NewRetryPolicy(w.cfg.Backoff)
	now := time.Now().UTC()
	updatedJob, update, err := job.MarkFailed(now, policy, failure)
	if err != nil {
		logger.Error("failed to compute panic failure update", zap.Error(err))
		return
	}

	repoCtx := context.WithoutCancel(ctx)
	if err := w.repo.MarkFailed(repoCtx, updatedJob, update); err != nil {
		logger.Error("failed to mark job as failed after panic", zap.Error(err))
	}
}
