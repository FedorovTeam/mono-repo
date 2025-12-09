package worker

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"
	"time"

	"github.com/mono-repo/docling-transformer-service/internal/converter"
	"github.com/mono-repo/docling-transformer-service/internal/repository"
	"github.com/mono-repo/docling-transformer-service/internal/storage"
	"github.com/mono-repo/docling-transformer-service/internal/telemetry"
	"github.com/mono-repo/docling-transformer-service/internal/transformations"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/metric"
	"go.uber.org/zap"
)

// Config задаёт параметры воркера очереди.
type Config struct {
	MarkdownBucket       string
	MaxWorkers           int
	PollInterval         time.Duration
	ProcessingTimeout    time.Duration
	MaxProcessingRetries int
	CleanupInterval      time.Duration
	CleanupRetention     time.Duration
}

type workerMetrics struct {
	pendingGauge    metric.Int64ObservableGauge
	processingGauge metric.Int64ObservableGauge
	restoredCounter metric.Int64Counter
	rejectedCounter metric.Int64Counter
	durationHist    metric.Float64Histogram
	mu              sync.Mutex
	lastPending     int64
	lastProcessing  int64
}

type Processor struct {
	repo    repository.QueueRepository
	conv    converter.Converter
	storage storage.ObjectStorage
	logger  *zap.Logger
	cfg     Config
	metrics *workerMetrics
}

func NewProcessor(repo repository.QueueRepository, conv converter.Converter, storage storage.ObjectStorage, logger *zap.Logger, cfg Config) *Processor {
	if logger == nil {
		logger = zap.NewNop()
	}

	return &Processor{repo: repo, conv: conv, storage: storage, logger: logger, cfg: cfg, metrics: newWorkerMetrics(logger)}
}

func newWorkerMetrics(logger *zap.Logger) *workerMetrics {
	meter := otel.GetMeterProvider().Meter("docling-transformer-service/worker")
	m := &workerMetrics{}

	var err error
	m.pendingGauge, err = meter.Int64ObservableGauge("docling_transformer_queue_pending")
	if err != nil && logger != nil {
		logger.Warn("failed to create pending gauge", zap.Error(err))
	}
	m.processingGauge, err = meter.Int64ObservableGauge("docling_transformer_queue_processing")
	if err != nil && logger != nil {
		logger.Warn("failed to create processing gauge", zap.Error(err))
	}
	m.restoredCounter, err = meter.Int64Counter("docling_transformer_tasks_restored")
	if err != nil && logger != nil {
		logger.Warn("failed to create restored counter", zap.Error(err))
	}
	m.rejectedCounter, err = meter.Int64Counter("docling_transformer_tasks_rejected")
	if err != nil && logger != nil {
		logger.Warn("failed to create rejected counter", zap.Error(err))
	}
	m.durationHist, err = meter.Float64Histogram("docling_transformer_processing_duration_ms")
	if err != nil && logger != nil {
		logger.Warn("failed to create duration histogram", zap.Error(err))
	}

	if m.pendingGauge != nil && m.processingGauge != nil {
		if _, err := meter.RegisterCallback(func(ctx context.Context, observer metric.Observer) error {
			m.mu.Lock()
			defer m.mu.Unlock()
			observer.ObserveInt64(m.pendingGauge, m.lastPending)
			observer.ObserveInt64(m.processingGauge, m.lastProcessing)
			return nil
		}, m.pendingGauge, m.processingGauge); err != nil && logger != nil {
			logger.Warn("failed to register queue callback", zap.Error(err))
		}
	}

	return m
}

func (m *workerMetrics) recordQueueSizes(pending, processing int64) {
	if m == nil {
		return
	}

	m.mu.Lock()
	m.lastPending = pending
	m.lastProcessing = processing
	m.mu.Unlock()
}

func (m *workerMetrics) recordRecoveries(ctx context.Context, restored, failed int) {
	if m == nil {
		return
	}

	if restored > 0 && m.restoredCounter != nil {
		m.restoredCounter.Add(ctx, int64(restored))
	}

	if failed > 0 && m.rejectedCounter != nil {
		m.rejectedCounter.Add(ctx, int64(failed))
	}
}

func (m *workerMetrics) recordDuration(ctx context.Context, duration time.Duration) {
	if m == nil || m.durationHist == nil {
		return
	}

	m.durationHist.Record(ctx, float64(duration.Milliseconds()))
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

	maintenanceInterval := p.cfg.CleanupInterval
	if maintenanceInterval <= 0 {
		maintenanceInterval = 30 * time.Second
	}

	for i := 0; i < workers; i++ {
		go p.workerLoop(ctx, interval)
	}

	go p.maintenanceLoop(ctx, maintenanceInterval)
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

		start := time.Now()
		if task.StartedAt != nil {
			start = *task.StartedAt
		}

		taskCtx, correlationID := telemetry.ContextWithCorrelationID(ctx, task.CorrelationID)
		p.logger.Info("processing transformation", zap.String("queue_id", task.ID.String()), zap.String("correlation_id", correlationID))

		if err := p.handleTask(taskCtx, task); err != nil {
			p.metrics.recordDuration(taskCtx, time.Since(start))
			p.logger.Error("transformation failed", zap.String("queue_id", task.ID.String()), zap.String("correlation_id", correlationID), zap.Duration("duration", time.Since(start)), zap.Error(err))
			if markErr := p.repo.MarkFailed(ctx, task.ID, err.Error()); markErr != nil {
				p.logger.Error("mark failed errored", zap.String("queue_id", task.ID.String()), zap.String("correlation_id", correlationID), zap.Error(markErr))
			}
		} else {
			duration := time.Since(start)
			p.metrics.recordDuration(taskCtx, duration)
			p.logger.Info("transformation completed", zap.String("queue_id", task.ID.String()), zap.String("correlation_id", correlationID), zap.Duration("duration", duration))
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

func (p *Processor) maintenanceLoop(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			p.runMaintenanceCycle(ctx)
		}
	}
}

func (p *Processor) runMaintenanceCycle(ctx context.Context) {
	if pending, processing, err := p.repo.CountByStatus(ctx); err != nil {
		p.logger.Error("count tasks by status failed", zap.Error(err))
	} else {
		p.metrics.recordQueueSizes(int64(pending), int64(processing))
		p.logger.Info("queue status snapshot", zap.Int("pending", pending), zap.Int("processing", processing))
	}

	if p.cfg.ProcessingTimeout > 0 {
		restored, failed, err := p.repo.RestoreStuckProcessing(ctx, p.cfg.ProcessingTimeout, p.cfg.MaxProcessingRetries)
		if err != nil {
			p.logger.Error("restore stuck tasks failed", zap.Error(err))
		} else if restored > 0 || failed > 0 {
			p.logger.Warn("stuck tasks processed", zap.Int("restored", restored), zap.Int("failed", failed), zap.Duration("processing_timeout", p.cfg.ProcessingTimeout))
		}

		p.metrics.recordRecoveries(ctx, restored, failed)
	}

	if p.cfg.CleanupRetention > 0 {
		removed, err := p.repo.CleanupFinished(ctx, p.cfg.CleanupRetention)
		if err != nil {
			p.logger.Error("cleanup finished tasks failed", zap.Error(err))
		} else if removed > 0 {
			p.logger.Info("cleanup finished tasks completed", zap.Int("removed", removed), zap.Duration("retention", p.cfg.CleanupRetention))
		}
	}
}

func (p *Processor) wait(ctx context.Context, interval time.Duration) {
	select {
	case <-ctx.Done():
	case <-time.After(interval):
	}
}
