package preprocess

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"go.uber.org/zap"

	"preprocess-service/internal/domain"
	"preprocess-service/internal/usecase/ports"
)

type Config struct {
	LibreOfficePath string
	WorkingDir      string
	OutputDir       string
	TargetBucket    string
	TargetMediaType string
	ProcessTimeout  time.Duration
	ConvertTimeout  time.Duration
	Backoff         time.Duration
}

type Service struct {
	repo       domain.PreprocessJobRepository
	downloader ports.StorageDownloader
	uploader   ports.StorageUploader
	converter  ports.DocumentConverter
	workspace  ports.WorkspaceManager
	callback   ports.CallbackSender
	cfg        Config
	logger     *zap.SugaredLogger
	policy     domain.RetryPolicy

	downloadSourceFn func(ctx context.Context, job Job, workspace ports.Workspace) (string, error)
	convertFn        func(ctx context.Context, sourcePath string, workspace ports.Workspace) error
	uploadResultFn   func(ctx context.Context, bucket, key, path string) error
}

func NewService(repo domain.PreprocessJobRepository, downloader ports.StorageDownloader, uploader ports.StorageUploader, converter ports.DocumentConverter, workspace ports.WorkspaceManager, callback ports.CallbackSender, cfg Config, logger *zap.SugaredLogger) (*Service, error) {
	if repo == nil {
		return nil, fmt.Errorf("job repository is required")
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
	if workspace == nil {
		return nil, fmt.Errorf("workspace manager is required")
	}

	normalized := normalizeConfig(cfg)

	if logger == nil {
		logger = zap.NewNop().Sugar()
	}

	svc := &Service{
		repo:       repo,
		downloader: downloader,
		uploader:   uploader,
		converter:  converter,
		workspace:  workspace,
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
	if cfg.ProcessTimeout <= 0 {
		cfg.ProcessTimeout = cfg.ConvertTimeout + time.Minute
	}
	if cfg.Backoff <= 0 {
		cfg.Backoff = 30 * time.Second
	}
	if cfg.TargetMediaType == "" {
		cfg.TargetMediaType = DefaultTargetMediaType
	}

	return cfg
}

func (s *Service) Process(ctx context.Context, job Job) error {
	logger := s.logger.With("job_id", job.ID, "correlation_id", job.CorrelationID)
	logger.Info("start processing job")

	processCtx, cancel := context.WithTimeout(ctx, s.cfg.ProcessTimeout)
	defer cancel()

	jobState := job

	if err := s.handleJob(processCtx, &jobState); err != nil {
		return s.handleFailure(processCtx, logger, jobState, err)
	}
	if err := processCtx.Err(); err != nil {
		return s.handleFailure(processCtx, logger, jobState, err)
	}

	logger.Info("job completed successfully")
	jobState.TargetMediaType = s.cfg.TargetMediaType
	now := time.Now().UTC()
	completedJob, err := jobState.MarkCompleted(now)
	if err != nil {
		logger.Errorw("failed to compute completion state", "error", err)
		return err
	}
	if err := s.repo.MarkDone(processCtx, completedJob); err != nil {
		logger.Errorw("failed to mark job done", "error", err)
		return err
	}

	s.notifyCallback(processCtx, completedJob, StatusDone, callbackErrorInfo{})
	return nil
}

func (s *Service) handleFailure(ctx context.Context, logger *zap.SugaredLogger, job Job, err error) error {
	logger.Errorw("job failed", "error", err)

	repoCtx := context.WithoutCancel(ctx)

	failure := s.failureFromError(job, err)
	now := time.Now().UTC()
	updatedJob, update, failErr := job.MarkFailed(now, s.policy, failure)
	if failErr != nil {
		logger.Errorw("failed to compute failure update", "error", failErr)
		return err
	}

	if err := s.repo.MarkFailed(repoCtx, updatedJob, update); err != nil {
		logger.Errorw("failed to update job status", "error", err)
	}

	if update.Status == StatusStopped {
		s.notifyCallback(repoCtx, updatedJob, StatusStopped, callbackErrorInfo{Code: update.Code, Message: update.Message})
	}

	return err
}

func (s *Service) handleJob(ctx context.Context, job *Job) error {
	s.ensureFunctions()

	workspace, err := s.workspace.Prepare(job.ID)
	if err != nil {
		return permanentError(domain.ErrorCodeTmpDir, fmt.Errorf("prepare workspace: %w", err))
	}
	defer workspace.Cleanup()

	sourcePath, err := s.downloadSourceFn(ctx, *job, workspace)
	if err != nil {
		return err
	}

	if err := s.convertFn(ctx, sourcePath, workspace); err != nil {
		return err
	}

	outputPath, err := workspace.OutputPath(job.SourceKey)
	if err != nil {
		if errors.Is(err, ports.ErrInvalidObjectKey) {
			return permanentError(domain.ErrorCodeSourceKeyInvalid, err)
		}
		return permanentError(domain.ErrorCodeOutputDir, fmt.Errorf("output path: %w", err))
	}
	if err := workspace.EnsureExists(outputPath); err != nil {
		return permanentError(domain.ErrorCodeConvertMissingOut, fmt.Errorf("output file not found: %w", err))
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

func (s *Service) downloadSource(ctx context.Context, job Job, workspace ports.Workspace) (string, error) {
	targetPath, err := workspace.SourcePath(job.SourceKey)
	if err != nil {
		if errors.Is(err, ports.ErrInvalidObjectKey) {
			return "", permanentError(domain.ErrorCodeSourceKeyInvalid, err)
		}
		return "", permanentError(domain.ErrorCodeTmpDir, fmt.Errorf("prepare source path: %w", err))
	}

	if err := s.downloader.Download(ctx, job.SourceBucket, job.SourceKey, job.SourceETag, targetPath); err != nil {
		if errors.Is(err, ports.ErrObjectNotFound) || errors.Is(err, ports.ErrPreconditionFailed) {
			return "", permanentError(domain.ErrorCodeSourceMissing, err)
		}
		return "", temporaryError(domain.ErrorCodeDownloadFailed, err)
	}

	return targetPath, nil
}

func (s *Service) convertToDocx(ctx context.Context, sourcePath string, workspace ports.Workspace) error {
	convertCtx, cancel := context.WithTimeout(ctx, s.cfg.ConvertTimeout)
	defer cancel()

	if err := s.converter.Convert(convertCtx, sourcePath, workspace.OutputDir()); err != nil {
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

		s.logger.Errorw("callback delivery failed", "attempt", attempt, "temporary", temporary, "error", err, "callback_url", job.CallbackURL)

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
