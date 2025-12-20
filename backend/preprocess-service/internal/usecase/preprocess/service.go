package preprocess

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"preprocess-service/internal/domain"
	"preprocess-service/internal/usecase/ports"
)

type Config struct {
	LibreOfficePath string
	WorkingDir      string
	OutputDir       string
	TargetBucket    string
	TargetMediaType string
	ConvertTimeout  time.Duration
	Backoff         time.Duration
}

type Service struct {
	repo       ports.QueueRepository
	downloader ports.StorageDownloader
	uploader   ports.StorageUploader
	converter  ports.DocumentConverter
	callback   ports.CallbackSender
	cfg        Config
	logger     *slog.Logger
	policy     domain.RetryPolicy

	downloadSourceFn func(ctx context.Context, job Job, dir string) (string, error)
	convertFn        func(ctx context.Context, sourcePath, outDir string) error
	uploadResultFn   func(ctx context.Context, bucket, key, path string) error
}

func NewService(repo ports.QueueRepository, downloader ports.StorageDownloader, uploader ports.StorageUploader, converter ports.DocumentConverter, callback ports.CallbackSender, cfg Config, logger *slog.Logger) (*Service, error) {
	if repo == nil {
		return nil, fmt.Errorf("queue repository is required")
	}
	if downloader == nil {
		return nil, fmt.Errorf("storage downloader is required")
	}
	if uploader == nil {
		return nil, fmt.Errorf("storage uploader is required")
	}
	if converter == nil {
		return nil, fmt.Errorf("document converter is required")
	}

	normalized := normalizeConfig(cfg)

	if logger == nil {
		logger = slog.Default()
	}

	if err := os.MkdirAll(normalized.WorkingDir, 0o755); err != nil {
		return nil, fmt.Errorf("create working dir: %w", err)
	}
	if err := os.MkdirAll(normalized.OutputDir, 0o755); err != nil {
		return nil, fmt.Errorf("create output dir: %w", err)
	}

	svc := &Service{
		repo:       repo,
		downloader: downloader,
		uploader:   uploader,
		converter:  converter,
		callback:   callback,
		cfg:        normalized,
		logger:     logger,
		policy:     domain.NewRetryPolicy(normalized.Backoff),
	}

	svc.downloadSourceFn = svc.downloadSource
	svc.convertFn = svc.convertToDocx
	svc.uploadResultFn = svc.uploadResult

	return svc, nil
}

func normalizeConfig(cfg Config) Config {
	if cfg.ConvertTimeout <= 0 {
		cfg.ConvertTimeout = 2 * time.Minute
	}
	if cfg.Backoff <= 0 {
		cfg.Backoff = 30 * time.Second
	}
	if cfg.TargetMediaType == "" {
		cfg.TargetMediaType = DefaultTargetMediaType
	}
	if cfg.WorkingDir == "" {
		cfg.WorkingDir = filepath.Join(os.TempDir(), "preprocess")
	}
	if cfg.OutputDir == "" {
		cfg.OutputDir = filepath.Join(cfg.WorkingDir, "output")
	}

	return cfg
}

func (s *Service) Process(ctx context.Context, job Job) error {
	logger := s.logger.With("job_id", job.JobID, "correlation_id", job.CorrelationID)
	logger.Info("start processing job")

	jobState := job

	if err := s.handleJob(ctx, &jobState); err != nil {
		logger.Error("job failed", "error", err)
		update := s.failureDecision(jobState, err)
		if err := s.repo.MarkFailed(ctx, jobState, update); err != nil {
			logger.Error("failed to update job status", "error", err)
		}
		if update.Status == StatusStopped {
			s.notifyCallback(ctx, jobState, StatusStopped, callbackErrorInfo{Code: update.Code, Message: update.Message})
		}
		return err
	}

	logger.Info("job completed successfully")
	if err := s.repo.MarkDone(ctx, jobState, s.cfg.TargetMediaType); err != nil {
		logger.Error("failed to mark job done", "error", err)
		return err
	}

	s.notifyCallback(ctx, jobState, StatusDone, callbackErrorInfo{})
	return nil
}

func (s *Service) handleJob(ctx context.Context, job *Job) error {
	s.ensureFunctions()

	tmpDir, err := os.MkdirTemp(s.cfg.WorkingDir, fmt.Sprintf("job-%s-", job.JobID))
	if err != nil {
		return permanentError("tmp_dir", fmt.Errorf("create temp dir: %w", err))
	}
	defer os.RemoveAll(tmpDir)

	outputDir := filepath.Join(s.cfg.OutputDir, job.JobID)
	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		return permanentError("output_dir", fmt.Errorf("prepare output dir: %w", err))
	}

	sourcePath, err := s.downloadSourceFn(ctx, *job, tmpDir)
	if err != nil {
		return err
	}

	if err := s.convertFn(ctx, sourcePath, outputDir); err != nil {
		return err
	}

	outputPath := s.outputFilePath(sourcePath, outputDir)
	if _, statErr := os.Stat(outputPath); statErr != nil {
		return permanentError("convert_missing_output", fmt.Errorf("output file not found: %w", statErr))
	}

	targetBucket := job.TargetBucket
	if targetBucket == "" {
		targetBucket = s.cfg.TargetBucket
	}
	job.TargetBucket = targetBucket

	if targetBucket == "" {
		return permanentError("target_bucket_missing", errors.New("target bucket is required"))
	}
	if job.TargetKey == "" {
		return permanentError("target_key_missing", errors.New("target key is required"))
	}

	if err := s.uploadResultFn(ctx, targetBucket, job.TargetKey, outputPath); err != nil {
		return err
	}

	return nil
}

func (s *Service) downloadSource(ctx context.Context, job Job, dir string) (string, error) {
	filename := filepath.Base(job.SourceKey)
	if filename == "" {
		return "", permanentError("source_key_invalid", errors.New("empty source key"))
	}

	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", permanentError("tmp_dir", fmt.Errorf("prepare dir: %w", err))
	}

	targetPath := filepath.Join(dir, filename)

	if err := s.downloader.Download(ctx, job.SourceBucket, job.SourceKey, job.SourceETag, targetPath); err != nil {
		if errors.Is(err, ports.ErrObjectNotFound) || errors.Is(err, ports.ErrPreconditionFailed) {
			return "", permanentError("source_missing", err)
		}
		return "", temporaryError("download_failed", err)
	}

	return targetPath, nil
}

func (s *Service) convertToDocx(ctx context.Context, sourcePath, outDir string) error {
	convertCtx, cancel := context.WithTimeout(ctx, s.cfg.ConvertTimeout)
	defer cancel()

	if err := s.converter.Convert(convertCtx, sourcePath, outDir); err != nil {
		if errors.Is(convertCtx.Err(), context.DeadlineExceeded) {
			return temporaryError("convert_timeout", fmt.Errorf("conversion timeout: %w", err))
		}
		return permanentError("convert_failed", err)
	}
	return nil
}

func (s *Service) uploadResult(ctx context.Context, bucket, key, path string) error {
	if err := s.uploader.Upload(ctx, bucket, key, path, s.cfg.TargetMediaType); err != nil {
		if errors.Is(err, ports.ErrBucketNotFound) {
			return permanentError("target_bucket_missing", err)
		}
		return temporaryError("upload_failed", err)
	}
	return nil
}

func (s *Service) failureDecision(job Job, err error) FailureUpdate {
	var typed jobError
	code := string(domain.ErrorCodeProcessingError)
	permanent := false

	if errors.As(err, &typed) {
		code = typed.Code
		permanent = typed.Permanent
		err = typed.Unwrap()
	}

	message := err.Error()

	if s.policy.ShouldStop(job.Attempts, job.MaxAttempts, permanent) {
		return FailureUpdate{
			Status:    StatusStopped,
			NextRunAt: time.Now().UTC(),
			Code:      code,
			Message:   message,
			Permanent: permanent,
			Details: map[string]any{
				"code":      code,
				"permanent": permanent,
				"attempt":   job.Attempts,
			},
			TargetBucket: job.TargetBucket,
			TargetKey:    job.TargetKey,
		}
	}

	return FailureUpdate{
		Status:    StatusFailed,
		NextRunAt: s.policy.NextRun(time.Now(), job.Attempts),
		Code:      code,
		Message:   message,
		Permanent: permanent,
		Details: map[string]any{
			"code":      code,
			"permanent": permanent,
			"attempt":   job.Attempts,
		},
		TargetBucket: job.TargetBucket,
		TargetKey:    job.TargetKey,
	}
}

func (s *Service) notifyCallback(ctx context.Context, job Job, status string, errInfo callbackErrorInfo) {
	if job.CallbackURL == "" || s.callback == nil {
		return
	}

	payload := ports.CallbackPayload{
		JobID:        job.JobID,
		Status:       status,
		TargetBucket: job.TargetBucket,
		TargetKey:    job.TargetKey,
	}

	if errInfo.Code != "" || errInfo.Message != "" {
		payload.Error = &ports.CallbackErrorPayload{Code: errInfo.Code, Message: errInfo.Message}
	}

	backoff := s.policy.Delay(1)
	if backoff <= 0 {
		backoff = time.Second
	}

	for attempt := 1; attempt <= callbackMaxAttempts; attempt++ {
		temporary, err := s.callback.Send(ctx, job.CallbackURL, payload)
		if err == nil {
			return
		}

		s.logger.Error("callback delivery failed", "attempt", attempt, "temporary", temporary, "error", err, "callback_url", job.CallbackURL)

		if !temporary || attempt == callbackMaxAttempts {
			return
		}

		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
	}
}

func (s *Service) outputFilePath(sourcePath, outDir string) string {
	base := filepath.Base(sourcePath)
	ext := filepath.Ext(base)
	name := strings.TrimSuffix(base, ext)
	if name == "" {
		name = base
	}
	return filepath.Join(outDir, name+".docx")
}

func (s *Service) ensureFunctions() {
	if s.downloadSourceFn == nil {
		s.downloadSourceFn = s.downloadSource
	}
	if s.convertFn == nil {
		s.convertFn = s.convertToDocx
	}
	if s.uploadResultFn == nil {
		s.uploadResultFn = s.uploadResult
	}
}
