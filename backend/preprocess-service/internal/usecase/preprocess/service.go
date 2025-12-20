package preprocess

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
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
	logger := s.logger.With("job_id", job.ID, "correlation_id", job.CorrelationID)
	logger.Info("start processing job")

	jobState := job

	if err := s.handleJob(ctx, &jobState); err != nil {
		logger.Error("job failed", "error", err)
		failure := s.failureFromError(jobState, err)
		now := time.Now().UTC()
		updatedJob, update, failErr := jobState.MarkFailed(now, s.policy, failure)
		if failErr != nil {
			logger.Error("failed to compute failure update", "error", failErr)
			return err
		}
		jobState = updatedJob
		if err := s.repo.MarkFailed(ctx, jobState, update); err != nil {
			logger.Error("failed to update job status", "error", err)
		}
		if update.Status == StatusStopped {
			s.notifyCallback(ctx, jobState, StatusStopped, callbackErrorInfo{Code: update.Code, Message: update.Message})
		}
		return err
	}

	logger.Info("job completed successfully")
	jobState.TargetMediaType = s.cfg.TargetMediaType
	now := time.Now().UTC()
	completedJob, err := jobState.MarkCompleted(now)
	if err != nil {
		logger.Error("failed to compute completion state", "error", err)
		return err
	}
	if err := s.repo.MarkDone(ctx, completedJob); err != nil {
		logger.Error("failed to mark job done", "error", err)
		return err
	}

	s.notifyCallback(ctx, completedJob, StatusDone, callbackErrorInfo{})
	return nil
}

func (s *Service) handleJob(ctx context.Context, job *Job) error {
	s.ensureFunctions()

	tmpDir, err := os.MkdirTemp(s.cfg.WorkingDir, fmt.Sprintf("job-%s-", job.ID))
	if err != nil {
		return permanentError(domain.ErrorCodeTmpDir, fmt.Errorf("create temp dir: %w", err))
	}
	defer os.RemoveAll(tmpDir)

	outputDir := filepath.Join(s.cfg.OutputDir, job.ID)
	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		return permanentError(domain.ErrorCodeOutputDir, fmt.Errorf("prepare output dir: %w", err))
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
		return permanentError(domain.ErrorCodeConvertMissingOut, fmt.Errorf("output file not found: %w", statErr))
	}

	targetBucket := job.TargetBucket
	if targetBucket == "" {
		targetBucket = s.cfg.TargetBucket
	}
	job.TargetBucket = targetBucket

	if targetBucket == "" {
		return permanentError(domain.ErrorCodeTargetBucket, errors.New("target bucket is required"))
	}
	if job.TargetKey == "" {
		return permanentError(domain.ErrorCodeTargetKey, errors.New("target key is required"))
	}

	if err := s.uploadResultFn(ctx, targetBucket, job.TargetKey, outputPath); err != nil {
		return err
	}

	return nil
}

func (s *Service) downloadSource(ctx context.Context, job Job, dir string) (string, error) {
	filename := filepath.Base(job.SourceKey)
	if filename == "" {
		return "", permanentError(domain.ErrorCodeSourceKeyInvalid, errors.New("empty source key"))
	}

	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", permanentError(domain.ErrorCodeTmpDir, fmt.Errorf("prepare dir: %w", err))
	}

	targetPath := filepath.Join(dir, filename)

	if err := s.downloader.Download(ctx, job.SourceBucket, job.SourceKey, job.SourceETag, targetPath); err != nil {
		if errors.Is(err, ports.ErrObjectNotFound) || errors.Is(err, ports.ErrPreconditionFailed) {
			return "", permanentError(domain.ErrorCodeSourceMissing, err)
		}
		return "", temporaryError(domain.ErrorCodeDownloadFailed, err)
	}

	return targetPath, nil
}

func (s *Service) convertToDocx(ctx context.Context, sourcePath, outDir string) error {
	convertCtx, cancel := context.WithTimeout(ctx, s.cfg.ConvertTimeout)
	defer cancel()

	if err := s.converter.Convert(convertCtx, sourcePath, outDir); err != nil {
		if errors.Is(convertCtx.Err(), context.DeadlineExceeded) {
			return temporaryError(domain.ErrorCodeConvertTimeout, fmt.Errorf("conversion timeout: %w", err))
		}
		return permanentError(domain.ErrorCodeConvertFailed, err)
	}
	return nil
}

func (s *Service) uploadResult(ctx context.Context, bucket, key, path string) error {
	if err := s.uploader.Upload(ctx, bucket, key, path, s.cfg.TargetMediaType); err != nil {
		if errors.Is(err, ports.ErrBucketNotFound) {
			return permanentError(domain.ErrorCodeTargetBucket, err)
		}
		return temporaryError(domain.ErrorCodeUploadFailed, err)
	}
	return nil
}

func (s *Service) notifyCallback(ctx context.Context, job Job, status domain.PreprocessStatus, errInfo callbackErrorInfo) {
	if job.CallbackURL == "" || s.callback == nil {
		return
	}

	payload := ports.CallbackPayload{
		JobID:        job.ID,
		Status:       status.String(),
		TargetBucket: job.TargetBucket,
		TargetKey:    job.TargetKey,
	}

	if errInfo.Code != "" || errInfo.Message != "" {
		payload.Error = &ports.CallbackErrorPayload{Code: errInfo.Code.String(), Message: errInfo.Message}
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

func (s *Service) failureFromError(job Job, err error) domain.Failure {
	var typed jobError
	failure := domain.Failure{
		Code:      domain.ErrorCodeProcessingError,
		Message:   err.Error(),
		Permanent: false,
		Details: map[string]string{
			"attempt": strconv.Itoa(job.Attempts),
		},
	}

	if errors.As(err, &typed) {
		failure.Code = typed.Code
		failure.Permanent = typed.Permanent
		failure.Message = typed.Error()
	}

	failure.Details["code"] = failure.Code.String()
	failure.Details["permanent"] = strconv.FormatBool(failure.Permanent)

	return failure
}
