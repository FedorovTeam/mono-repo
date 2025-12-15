package usecase

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"time"

	"github.com/mono-repo/docling-transformer-service/internal/domain"
	transformations "github.com/mono-repo/docling-transformer-service/internal/domain/transformations"
	repo "github.com/mono-repo/docling-transformer-service/internal/repository"
	"github.com/mono-repo/docling-transformer-service/internal/usecase/metrics"
	"go.uber.org/zap"
)

type TaskState string

const (
	StateProcessing     TaskState = "processing"
	StateWaitingDocling           = "waiting_docling"
	StateCompleted                = "completed"
)

type ConversionExecutorConfig struct {
	MarkdownBucket       string
	FastOperationTimeout time.Duration
	HTTPClient           HTTPClient
	UseSourceAsync       bool
}

type ConversionExecutor struct {
	repo       repo.QueueRepository
	conv       domain.Converter
	storage    domain.ObjectStorage
	poller     *DoclingPoller
	metrics    *metrics.WorkerMetrics
	logger     *zap.Logger
	httpClient HTTPClient
	cfg        ConversionExecutorConfig
}

func NewConversionExecutor(repository repo.QueueRepository, conv domain.Converter, storage domain.ObjectStorage, poller *DoclingPoller, metrics *metrics.WorkerMetrics, logger *zap.Logger, cfg ConversionExecutorConfig) *ConversionExecutor {
	if logger == nil {
		logger = zap.NewNop()
	}

	client := cfg.HTTPClient
	if client == nil {
		timeout := cfg.FastOperationTimeout
		if timeout <= 0 {
			timeout = 30 * time.Second
		}
		client = &http.Client{Timeout: timeout}
	}

	return &ConversionExecutor{repo: repository, conv: conv, storage: storage, poller: poller, metrics: metrics, logger: logger, httpClient: client, cfg: cfg}
}

func (e *ConversionExecutor) ProcessTask(ctx context.Context, task *transformations.TransformationTask) (TaskState, error) {
	if task.MarkdownBucket == "" {
		task.MarkdownBucket = e.cfg.MarkdownBucket
	}
	if task.MarkdownBucket == "" {
		return StateProcessing, fmt.Errorf("markdown bucket is empty")
	}

	if task.MarkdownObjectKey == "" {
		task.MarkdownObjectKey = domain.BuildMarkdownKey(task.OriginalObjectKey)
	}

	if task.DoclingJobID != "" {
		return e.pollDocling(ctx, task)
	}

	return e.submitConversion(ctx, task)
}

func (e *ConversionExecutor) pollDocling(ctx context.Context, task *transformations.TransformationTask) (TaskState, error) {
	result, err := e.poller.Poll(ctx, task)
	if err != nil {
		if errors.Is(err, ErrDoclingWaiting) {
			return StateWaitingDocling, nil
		}
		return StateProcessing, err
	}

	mediaType := normalizeMediaType(result.MediaType, result.Charset)

	uploadCtx, cancelUpload := e.fastPhaseContext(ctx)
	defer cancelUpload()

	if err := e.storage.Upload(uploadCtx, task.MarkdownBucket, task.MarkdownObjectKey, mediaType, result.Content); err != nil {
		return StateProcessing, fmt.Errorf("upload markdown: %w", err)
	}

	if err := e.repo.MarkDone(ctx, task.ID, task.MarkdownBucket, task.MarkdownObjectKey); err != nil {
		return StateProcessing, fmt.Errorf("mark done: %w", err)
	}

	return StateCompleted, nil
}

func (e *ConversionExecutor) submitConversion(ctx context.Context, task *transformations.TransformationTask) (TaskState, error) {
	useSourceAsync := e.cfg.UseSourceAsync && task.SourceURL == ""
	if !useSourceAsync && task.SourceURL == "" && task.SourceBucket != "" && task.SourceObjectKey != "" && task.OriginalBucket != "" {
		useSourceAsync = true
	}
	fileExt := task.FilenameExtension
	if fileExt == "" {
		key := task.OriginalObjectKey
		if task.SourceObjectKey != "" {
			key = task.SourceObjectKey
		}
		fileExt = domain.NormalizeExtension(filepath.Ext(key))
	}
	if fileExt == "" {
		fileExt = domain.NormalizeExtension(filepath.Ext(ResolveFilenameFromURL(task.SourceURL, "")))
	}

	contentType, err := domain.ResolveContentTypeByExtension(fileExt)
	if err != nil {
		return StateProcessing, err
	}

	var sourceContent []byte
	if !useSourceAsync {
		downloadCtx, cancelDownload := e.fastPhaseContext(ctx)
		defer cancelDownload()

		sourceContent, err = e.downloadSource(downloadCtx, task)
		if err != nil {
			return StateProcessing, err
		}
	}

	originalFileName := task.OriginalObjectKey
	if task.SourceObjectKey != "" {
		originalFileName = task.SourceObjectKey
	}
	originalFileName = ResolveFilenameFromURL(task.SourceURL, filepath.Base(originalFileName))
	if filepath.Ext(originalFileName) == "" && fileExt != "" {
		originalFileName = fmt.Sprintf("document.%s", fileExt)
	}

	submitCtx, cancelSubmit := e.fastPhaseContext(ctx)
	defer cancelSubmit()

	sourceBucket := task.OriginalBucket
	sourceObjectKey := task.OriginalObjectKey
	if task.SourceBucket != "" && task.SourceObjectKey != "" {
		sourceBucket = task.SourceBucket
		sourceObjectKey = task.SourceObjectKey
	}

	if useSourceAsync && (sourceBucket == "" || sourceObjectKey == "") {
		return StateProcessing, fmt.Errorf("source bucket/object key are required for source_async mode")
	}

	convertReq := domain.ConvertRequest{
		FileName:        originalFileName,
		Content:         sourceContent,
		ContentType:     contentType,
		SourceBucket:    sourceBucket,
		SourceObjectKey: sourceObjectKey,
		TargetBucket:    task.MarkdownBucket,
		TargetObjectKey: task.MarkdownObjectKey,
	}

	jobID, err := e.conv.Submit(submitCtx, convertReq)
	if err != nil {
		if state, handleErr, handled := e.handleTemporarySubmitError(ctx, task, err); handled {
			return state, handleErr
		}

		return StateProcessing, fmt.Errorf("docling submit: %w", err)
	}

	nextPoll := e.poller.NextPollTime(time.Now(), 0)
	if err := e.repo.MarkDoclingWaiting(ctx, task.ID, jobID, 0, nextPoll); err != nil {
		return StateProcessing, fmt.Errorf("store docling job: %w", err)
	}

	task.DoclingJobID = jobID
	task.DoclingPollCount = 0
	task.NextPollAt = &nextPoll

	e.logger.Info("docling job submitted", zap.String("job_id", jobID), zap.Time("next_poll_at", nextPoll))

	return StateWaitingDocling, nil
}

func (e *ConversionExecutor) downloadSource(ctx context.Context, task *transformations.TransformationTask) ([]byte, error) {
	if task.SourceURL != "" {
		return e.downloadFromURL(ctx, task.SourceURL)
	}

	if task.SourceBucket != "" && task.SourceObjectKey != "" {
		content, err := e.storage.Download(ctx, task.SourceBucket, task.SourceObjectKey)
		if err != nil {
			return nil, fmt.Errorf("download source object: %w", err)
		}

		return content, nil
	}

	content, err := e.storage.Download(ctx, task.OriginalBucket, task.OriginalObjectKey)
	if err != nil {
		return nil, fmt.Errorf("download original: %w", err)
	}

	return content, nil
}

func (e *ConversionExecutor) downloadFromURL(ctx context.Context, rawURL string) ([]byte, error) {
	if e.httpClient == nil {
		return nil, fmt.Errorf("download source: http client is not configured")
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, fmt.Errorf("download source: build request: %w", err)
	}

	resp, err := e.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("download source: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return nil, fmt.Errorf("download source: status %d: %s", resp.StatusCode, string(body))
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("download source: read body: %w", err)
	}

	if len(data) == 0 {
		return nil, fmt.Errorf("download source: empty body")
	}

	return data, nil
}

func (e *ConversionExecutor) fastPhaseContext(ctx context.Context) (context.Context, context.CancelFunc) {
	if e.cfg.FastOperationTimeout <= 0 {
		return ctx, func() {}
	}

	return context.WithTimeout(ctx, e.cfg.FastOperationTimeout)
}

func (e *ConversionExecutor) handleTemporarySubmitError(ctx context.Context, task *transformations.TransformationTask, err error) (TaskState, error, bool) {
	var tmpErr *domain.TemporarySubmitRetryExhaustedError
	if !errors.As(err, &tmpErr) {
		return StateProcessing, nil, false
	}

	if e.poller == nil {
		return StateProcessing, fmt.Errorf("docling submit: %w", err), true
	}

	retryIndex := task.RetryCount
	if retryIndex < 0 {
		retryIndex = 0
	}

	nextPoll := e.poller.NextPollTime(time.Now(), retryIndex)
	if markErr := e.repo.MarkDoclingWaiting(ctx, task.ID, task.DoclingJobID, task.DoclingPollCount, nextPoll); markErr != nil {
		return StateProcessing, fmt.Errorf("store docling waiting: %w", markErr), true
	}

	task.NextPollAt = &nextPoll

	var statusCode int
	var coder domain.StatusCoder
	if errors.As(err, &coder) {
		statusCode = coder.StatusCode()
	}

	e.logger.Warn("docling submit temporary error, rescheduled",
		zap.String("queue_id", task.ID.String()),
		zap.Int("retry_attempt", retryIndex),
		zap.Int("status_code", statusCode),
		zap.Time("next_poll_at", nextPoll),
		zap.Error(err),
	)

	return StateWaitingDocling, nil, true
}
