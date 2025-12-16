package worker

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"golang.org/x/sync/semaphore"
	"log/slog"

	"preprocess-service/internal/config"
)

const (
	defaultTargetMediaType = "application/vnd.openxmlformats-officedocument.wordprocessingml.document"
	statusDone             = "done"
	statusFailed           = "failed"
	statusPending          = "pending"
	statusProcessing       = "processing"
	statusStopped          = "stopped"
)

// Config описывает настройки воркера предобработки.
type Config struct {
	LibreOfficePath string
	WorkingDir      string
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
	minio     *minio.Client
	cfg       Config
	sem       *semaphore.Weighted
	logger    *slog.Logger
	waitGroup sync.WaitGroup
}

func New(db *pgxpool.Pool, minioCfg config.MinioConfig, cfg Config, logger *slog.Logger) (*Worker, error) {
	if db == nil {
		return nil, fmt.Errorf("db pool is required")
	}

	minioClient, err := minio.New(minioCfg.Endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(minioCfg.AccessKey, minioCfg.SecretKey, ""),
		Secure: minioCfg.SSL,
		Region: minioCfg.Region,
	})
	if err != nil {
		return nil, fmt.Errorf("init minio client: %w", err)
	}

	if logger == nil {
		logger = slog.Default()
	}

	normalizedCfg := normalizeConfig(cfg)
	if normalizedCfg.LibreOfficePath == "" {
		return nil, fmt.Errorf("libreoffice path is required")
	}

	if err := os.MkdirAll(normalizedCfg.WorkingDir, 0o755); err != nil {
		return nil, fmt.Errorf("create working dir: %w", err)
	}

	return &Worker{
		db:     db,
		minio:  minioClient,
		cfg:    normalizedCfg,
		sem:    semaphore.NewWeighted(normalizedCfg.Concurrency),
		logger: logger,
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
	if cfg.TargetMediaType == "" {
		cfg.TargetMediaType = defaultTargetMediaType
	}
	if cfg.WorkingDir == "" {
		cfg.WorkingDir = filepath.Join(os.TempDir(), "preprocess")
	}

	return cfg
}

func (w *Worker) Run(ctx context.Context) error {
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
    `, statusPending, statusFailed)

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
    `, statusProcessing, job.JobID); err != nil {
		return QueueJob{}, false, err
	}

	if err := tx.Commit(ctx); err != nil {
		return QueueJob{}, false, err
	}

	return job, true, nil
}

func (w *Worker) processJob(ctx context.Context, job QueueJob) {
	logger := w.logger.With("job_id", job.JobID, "correlation_id", job.CorrelationID)
	logger.Info("start processing job")

	if err := w.handleJob(ctx, job); err != nil {
		logger.Error("job failed", "error", err)
		if updateErr := w.markFailed(ctx, job, err); updateErr != nil {
			logger.Error("failed to update job status", "error", updateErr)
		}
		return
	}

	logger.Info("job completed successfully")
	if err := w.markDone(ctx, job); err != nil {
		logger.Error("failed to mark job done", "error", err)
	}
}

func (w *Worker) handleJob(ctx context.Context, job QueueJob) error {
	tmpDir, err := os.MkdirTemp(w.cfg.WorkingDir, fmt.Sprintf("job-%s-", job.JobID))
	if err != nil {
		return permanentError("tmp_dir", fmt.Errorf("create temp dir: %w", err))
	}
	defer os.RemoveAll(tmpDir)

	sourcePath, err := w.downloadSource(ctx, job, tmpDir)
	if err != nil {
		return err
	}

	if err := w.convertToDocx(ctx, sourcePath, tmpDir); err != nil {
		return err
	}

	outputPath := w.outputFilePath(sourcePath, tmpDir)
	if _, statErr := os.Stat(outputPath); statErr != nil {
		return permanentError("convert_missing_output", fmt.Errorf("output file not found: %w", statErr))
	}

	targetBucket := job.TargetBucket
	if targetBucket == "" {
		targetBucket = w.cfg.TargetBucket
	}

	if targetBucket == "" {
		return permanentError("target_bucket_missing", errors.New("target bucket is required"))
	}
	if job.TargetKey == "" {
		return permanentError("target_key_missing", errors.New("target key is required"))
	}

	if err := w.uploadResult(ctx, targetBucket, job.TargetKey, outputPath); err != nil {
		return err
	}

	return nil
}

func (w *Worker) downloadSource(ctx context.Context, job QueueJob, dir string) (string, error) {
	filename := filepath.Base(job.SourceKey)
	if filename == "" {
		return "", permanentError("source_key_invalid", errors.New("empty source key"))
	}

	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", permanentError("tmp_dir", fmt.Errorf("prepare dir: %w", err))
	}

	targetPath := filepath.Join(dir, filename)

	opts := minio.GetObjectOptions{}
	if job.SourceETag != "" {
		if err := opts.SetMatchETag(job.SourceETag); err != nil {
			return "", permanentError("etag", fmt.Errorf("set etag: %w", err))
		}
	}

	if err := w.minio.FGetObject(ctx, job.SourceBucket, job.SourceKey, targetPath, opts); err != nil {
		var resp minio.ErrorResponse
		if errors.As(err, &resp) {
			switch resp.StatusCode {
			case 404, 412:
				return "", permanentError("source_missing", err)
			}
		}

		return "", temporaryError("download_failed", err)
	}

	return targetPath, nil
}

func (w *Worker) convertToDocx(ctx context.Context, sourcePath, outDir string) error {
	convertCtx, cancel := context.WithTimeout(ctx, w.cfg.ConvertTimeout)
	defer cancel()

	cmd := exec.CommandContext(convertCtx, w.cfg.LibreOfficePath, "--headless", "--convert-to", "docx", "--outdir", outDir, sourcePath)

	output, err := cmd.CombinedOutput()
	if err != nil {
		if errors.Is(convertCtx.Err(), context.DeadlineExceeded) {
			return temporaryError("convert_timeout", fmt.Errorf("conversion timeout: %w", err))
		}
		return permanentError("convert_failed", fmt.Errorf("libreoffice error: %w; output: %s", err, strings.TrimSpace(string(output))))
	}

	return nil
}

func (w *Worker) uploadResult(ctx context.Context, bucket, key, path string) error {
	opts := minio.PutObjectOptions{ContentType: w.cfg.TargetMediaType}
	_, err := w.minio.FPutObject(ctx, bucket, key, path, opts)
	if err != nil {
		var resp minio.ErrorResponse
		if errors.As(err, &resp) && resp.StatusCode == 404 {
			return permanentError("target_bucket_missing", err)
		}
		return temporaryError("upload_failed", err)
	}
	return nil
}

func (w *Worker) markDone(ctx context.Context, job QueueJob) error {
	finishedAt := time.Now().UTC()
	_, err := w.db.Exec(ctx, `
        UPDATE preprocess_queue
        SET status = $2, finished_at = $3, updated_at = $3, target_media_type = $4, error_code = NULL, error_message = NULL
        WHERE job_id = $1
    `, job.JobID, statusDone, finishedAt, w.cfg.TargetMediaType)
	return err
}

func (w *Worker) markFailed(ctx context.Context, job QueueJob, jobErr error) error {
	finishedAt := time.Now().UTC()
	status, nextRunAt, code, message := failureDecision(job, jobErr, w.cfg.Backoff)
	_, err := w.db.Exec(ctx, `
        UPDATE preprocess_queue
        SET status = $2, next_run_at = $3, finished_at = CASE WHEN $2 = $5 THEN $4 ELSE finished_at END,
            error_code = $6, error_message = $7, updated_at = $4
        WHERE job_id = $1
    `, job.JobID, status, nextRunAt, finishedAt, statusStopped, code, message)
	return err
}

func failureDecision(job QueueJob, err error, backoff time.Duration) (string, time.Time, string, string) {
	var typed jobError
	code := "processing_error"
	permanent := false

	if errors.As(err, &typed) {
		code = typed.Code
		permanent = typed.Permanent
		err = typed.Unwrap()
	}

	message := err.Error()

	if permanent || job.Attempts >= job.MaxAttempts {
		return statusStopped, time.Now().UTC(), code, message
	}

	delay := backoff * time.Duration(job.Attempts)
	if delay <= 0 {
		delay = backoff
	}

	return statusFailed, time.Now().Add(delay).UTC(), code, message
}

func (w *Worker) outputFilePath(sourcePath, outDir string) string {
	base := filepath.Base(sourcePath)
	ext := filepath.Ext(base)
	name := strings.TrimSuffix(base, ext)
	if name == "" {
		name = base
	}
	return filepath.Join(outDir, name+".docx")
}
