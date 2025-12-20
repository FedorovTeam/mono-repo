package worker

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/sync/semaphore"
	"log/slog"

	"preprocess-service/internal/usecase/ports"
	"preprocess-service/internal/usecase/preprocess"
)

// Config описывает настройки воркера предобработки.
type Config struct {
	LibreOfficePath string
	WorkingDir      string
	OutputDir       string
	TargetBucket    string
	TargetMediaType string
	Concurrency     int64
	ConvertTimeout  time.Duration
	Backoff         time.Duration
	PollInterval    time.Duration
}

type QueueJob struct {
	JobID         string
	SourceBucket  string
	SourceKey     string
	SourceETag    string
	TargetBucket  string
	TargetKey     string
	CallbackURL   string
	CorrelationID string
	Attempts      int
	MaxAttempts   int
}

type Worker struct {
	db        *pgxpool.Pool
	cfg       Config
	sem       *semaphore.Weighted
	logger    *slog.Logger
	waitGroup sync.WaitGroup
	processor *preprocess.Service
}

type Dependencies struct {
	Repo       ports.QueueRepository
	Downloader ports.StorageDownloader
	Uploader   ports.StorageUploader
	Converter  ports.DocumentConverter
	Callback   ports.CallbackSender
}

func New(db *pgxpool.Pool, deps Dependencies, cfg Config, logger *slog.Logger) (*Worker, error) {
	if db == nil {
		return nil, fmt.Errorf("db pool is required")
	}

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
	if deps.Callback == nil {
		return nil, fmt.Errorf("callback sender is required")
	}

	if logger == nil {
		logger = slog.Default()
	}

	normalizedCfg := normalizeConfig(cfg)

	serviceCfg := preprocess.Config{
		LibreOfficePath: normalizedCfg.LibreOfficePath,
		WorkingDir:      normalizedCfg.WorkingDir,
		OutputDir:       normalizedCfg.OutputDir,
		TargetBucket:    normalizedCfg.TargetBucket,
		TargetMediaType: normalizedCfg.TargetMediaType,
		ConvertTimeout:  normalizedCfg.ConvertTimeout,
		Backoff:         normalizedCfg.Backoff,
	}

	processor, err := preprocess.NewService(deps.Repo, deps.Downloader, deps.Uploader, deps.Converter, deps.Callback, serviceCfg, logger)
	if err != nil {
		return nil, err
	}

	return &Worker{
		db:        db,
		cfg:       normalizedCfg,
		sem:       semaphore.NewWeighted(normalizedCfg.Concurrency),
		logger:    logger,
		processor: processor,
	}, nil
}

func normalizeConfig(cfg Config) Config {
	if cfg.Concurrency <= 0 {
		cfg.Concurrency = 1
	}
	if cfg.ConvertTimeout <= 0 {
		cfg.ConvertTimeout = 2 * time.Minute
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

		job, found, err := w.pickJob(ctx)
		if err != nil {
			w.logger.Error("failed to pick job", "error", err)
			select {
			case <-ctx.Done():
				w.waitGroup.Wait()
				return ctx.Err()
			case <-ticker.C:
			}
			continue
		}

		if !found {
			select {
			case <-ctx.Done():
				w.waitGroup.Wait()
				return ctx.Err()
			case <-ticker.C:
			}
			continue
		}

		if err := w.sem.Acquire(ctx, 1); err != nil {
			return err
		}

		w.waitGroup.Add(1)
		go func(job QueueJob) {
			defer w.waitGroup.Done()
			defer w.sem.Release(1)
			w.processJob(ctx, job)
		}(job)
	}
}

func (w *Worker) pickJob(ctx context.Context) (QueueJob, bool, error) {
	tx, err := w.db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return QueueJob{}, false, err
	}
	defer tx.Rollback(ctx)

	row := tx.QueryRow(ctx, `
        SELECT job_id, source_bucket, source_key, source_etag, target_bucket, target_key, attempts, max_attempts, callback_url, correlation_id
        FROM preprocess_queue
        WHERE status IN ($1, $2) AND next_run_at <= now()
        ORDER BY next_run_at
        LIMIT 1
        FOR UPDATE SKIP LOCKED
    `, preprocess.StatusPending, preprocess.StatusFailed)

	var job QueueJob
	err = row.Scan(&job.JobID, &job.SourceBucket, &job.SourceKey, &job.SourceETag, &job.TargetBucket, &job.TargetKey, &job.Attempts, &job.MaxAttempts, &job.CallbackURL, &job.CorrelationID)
	if errors.Is(err, pgx.ErrNoRows) {
		return QueueJob{}, false, tx.Commit(ctx)
	}
	if err != nil {
		return QueueJob{}, false, err
	}

	job.Attempts++

	if _, err := tx.Exec(ctx, `
        UPDATE preprocess_queue
        SET status = $1, attempts = attempts + 1, started_at = now(), updated_at = now()
        WHERE job_id = $2
    `, preprocess.StatusProcessing, job.JobID); err != nil {
		return QueueJob{}, false, err
	}

	if err := tx.Commit(ctx); err != nil {
		return QueueJob{}, false, err
	}

	return job, true, nil
}

func (w *Worker) processJob(ctx context.Context, job QueueJob) {
	w.ensureLogger()
	logger := w.logger.With("job_id", job.JobID, "correlation_id", job.CorrelationID)
	logger.Info("start processing job")

	if w.processor == nil {
		logger.Error("processor is not configured")
		return
	}

	if err := w.processor.Process(ctx, toUsecaseJob(job)); err != nil {
		logger.Error("job processing finished with error", "error", err)
	}
}

func (w *Worker) ensureLogger() {
	if w.logger == nil {
		w.logger = slog.Default()
	}
}

func toUsecaseJob(job QueueJob) preprocess.Job {
	return preprocess.Job{
		JobID:         job.JobID,
		SourceBucket:  job.SourceBucket,
		SourceKey:     job.SourceKey,
		SourceETag:    job.SourceETag,
		TargetBucket:  job.TargetBucket,
		TargetKey:     job.TargetKey,
		CallbackURL:   job.CallbackURL,
		CorrelationID: job.CorrelationID,
		Attempts:      job.Attempts,
		MaxAttempts:   job.MaxAttempts,
	}
}
