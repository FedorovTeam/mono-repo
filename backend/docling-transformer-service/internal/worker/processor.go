package worker

import (
	"context"
	"fmt"
	"path/filepath"
	"time"

	"github.com/mono-repo/docling-transformer-service/internal/converter"
	"github.com/mono-repo/docling-transformer-service/internal/repository"
	"github.com/mono-repo/docling-transformer-service/internal/storage"
	"github.com/mono-repo/docling-transformer-service/internal/telemetry"
	"github.com/mono-repo/docling-transformer-service/internal/transformations"
	"go.uber.org/zap"
)

// Config задаёт параметры воркера очереди.
type Config struct {
	MarkdownBucket string
	MaxWorkers     int
	PollInterval   time.Duration
}

type Processor struct {
	repo    repository.QueueRepository
	conv    converter.Converter
	storage storage.ObjectStorage
	logger  *zap.Logger
	cfg     Config
}

func NewProcessor(repo repository.QueueRepository, conv converter.Converter, storage storage.ObjectStorage, logger *zap.Logger, cfg Config) *Processor {
	if logger == nil {
		logger = zap.NewNop()
	}

	return &Processor{repo: repo, conv: conv, storage: storage, logger: logger, cfg: cfg}
}

func (p *Processor) Run(ctx context.Context) {
	workers := p.cfg.MaxWorkers
	if workers <= 0 {
		workers = 1
	}

	interval := p.cfg.PollInterval
	if interval <= 0 {
		interval = time.Second
	}

	for i := 0; i < workers; i++ {
		go p.workerLoop(ctx, interval)
	}
}

func (p *Processor) workerLoop(ctx context.Context, interval time.Duration) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		task, err := p.repo.AcquireNext(ctx)
		if err != nil {
			p.logger.Error("acquire next task failed", zap.Error(err))
			p.wait(ctx, interval)
			continue
		}

		if task == nil {
			p.wait(ctx, interval)
			continue
		}

		taskCtx, correlationID := telemetry.EnsureCorrelationID(ctx)
		p.logger.Info("processing transformation", zap.String("queue_id", task.ID.String()), zap.String("correlation_id", correlationID))

		if err := p.handleTask(taskCtx, task); err != nil {
			p.logger.Error("transformation failed", zap.String("queue_id", task.ID.String()), zap.String("correlation_id", correlationID), zap.Error(err))
			_ = p.repo.MarkFailed(ctx, task.ID, err.Error())
		} else {
			p.logger.Info("transformation completed", zap.String("queue_id", task.ID.String()), zap.String("correlation_id", correlationID))
		}
	}
}

func (p *Processor) handleTask(ctx context.Context, task *repository.TransformationTask) error {
	if task.MarkdownBucket == "" {
		task.MarkdownBucket = p.cfg.MarkdownBucket
	}
	if task.MarkdownBucket == "" {
		return fmt.Errorf("markdown bucket is empty")
	}

	if task.MarkdownObjectKey == "" {
		task.MarkdownObjectKey = transformations.BuildMarkdownKey(task.OriginalObjectKey)
	}

	fileExt := task.FilenameExtension
	if fileExt == "" {
		fileExt = transformations.NormalizeExtension(filepath.Ext(task.OriginalObjectKey))
	}

	contentType, err := converter.ResolveContentTypeByExtension(fileExt)
	if err != nil {
		return err
	}

	originalContent, err := p.storage.Download(ctx, task.OriginalBucket, task.OriginalObjectKey)
	if err != nil {
		return fmt.Errorf("download original: %w", err)
	}

	originalFileName := filepath.Base(task.OriginalObjectKey)
	if filepath.Ext(originalFileName) == "" && fileExt != "" {
		originalFileName = fmt.Sprintf("document.%s", fileExt)
	}

	result, err := p.conv.Convert(ctx, converter.DoclingConvertRequest{
		FileName:    originalFileName,
		Content:     originalContent,
		ContentType: contentType,
	})
	if err != nil {
		return fmt.Errorf("conversion error: %w", err)
	}

	mediaType := result.MediaType
	if mediaType == "" {
		mediaType = "text/markdown"
	}

	if err := p.storage.Upload(ctx, task.MarkdownBucket, task.MarkdownObjectKey, mediaType, result.Content); err != nil {
		return fmt.Errorf("upload markdown: %w", err)
	}

	if err := p.repo.MarkDone(ctx, task.ID, task.MarkdownBucket, task.MarkdownObjectKey); err != nil {
		return fmt.Errorf("mark done: %w", err)
	}

	return nil
}

func (p *Processor) wait(ctx context.Context, interval time.Duration) {
	select {
	case <-ctx.Done():
	case <-time.After(interval):
	}
}
