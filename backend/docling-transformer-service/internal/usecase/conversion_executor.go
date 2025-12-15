package usecase

import (
	"context"
	"errors"
	"fmt"
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
}

type ConversionExecutor struct {
	repo    repo.QueueRepository
	conv    domain.Converter
	storage domain.ObjectStorage
	poller  *DoclingPoller
	metrics *metrics.WorkerMetrics
	logger  *zap.Logger
	cfg     ConversionExecutorConfig
}

func NewConversionExecutor(repository repo.QueueRepository, conv domain.Converter, storage domain.ObjectStorage, poller *DoclingPoller, metrics *metrics.WorkerMetrics, logger *zap.Logger, cfg ConversionExecutorConfig) *ConversionExecutor {
	if logger == nil {
		logger = zap.NewNop()
	}

	return &ConversionExecutor{repo: repository, conv: conv, storage: storage, poller: poller, metrics: metrics, logger: logger, cfg: cfg}
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
	fileExt := task.FilenameExtension
	if fileExt == "" {
		fileExt = domain.NormalizeExtension(filepath.Ext(task.OriginalObjectKey))
	}

	contentType, err := domain.ResolveContentTypeByExtension(fileExt)
	if err != nil {
		return StateProcessing, err
	}

	downloadCtx, cancelDownload := e.fastPhaseContext(ctx)
	defer cancelDownload()

	originalContent, err := e.storage.Download(downloadCtx, task.OriginalBucket, task.OriginalObjectKey)
	if err != nil {
		return StateProcessing, fmt.Errorf("download original: %w", err)
	}

	originalFileName := filepath.Base(task.OriginalObjectKey)
	if filepath.Ext(originalFileName) == "" && fileExt != "" {
		originalFileName = fmt.Sprintf("document.%s", fileExt)
	}

	submitCtx, cancelSubmit := e.fastPhaseContext(ctx)
	defer cancelSubmit()

	jobID, err := e.conv.Submit(submitCtx, domain.ConvertRequest{
		FileName:    originalFileName,
		Content:     originalContent,
		ContentType: contentType,
	})
	if err != nil {
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

func (e *ConversionExecutor) fastPhaseContext(ctx context.Context) (context.Context, context.CancelFunc) {
	if e.cfg.FastOperationTimeout <= 0 {
		return ctx, func() {}
	}

	return context.WithTimeout(ctx, e.cfg.FastOperationTimeout)
}
