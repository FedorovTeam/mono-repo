package worker

import (
	"context"
	"errors"
	"fmt"
	"math"
	"path/filepath"
	"strings"
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
	MarkdownBucket         string
	MaxWorkers             int
	PollInterval           time.Duration
	ProcessingTimeout      time.Duration
	FastOperationTimeout   time.Duration
	MaxProcessingRetries   int
	CleanupInterval        time.Duration
	CleanupRetention       time.Duration
	DoclingInitialInterval time.Duration
	DoclingMaxInterval     time.Duration
	DoclingBackoffFactor   float64
	DoclingMaxAttempts     int
	DoclingMaxLifetime     time.Duration
}

type workerMetrics struct {
	pendingGauge    metric.Int64ObservableGauge
	processingGauge metric.Int64ObservableGauge
	doclingGauge    metric.Int64ObservableGauge
	restoredCounter metric.Int64Counter
	rejectedCounter metric.Int64Counter
	temporaryRetry  metric.Int64Counter
	durationHist    metric.Float64Histogram
	doclingWaitHist metric.Float64Histogram
	doclingPolls    metric.Int64Histogram
	mu              sync.Mutex
	lastPending     int64
	lastProcessing  int64
	lastDocling     int64
}

type Processor struct {
	repo    repository.QueueRepository
	conv    converter.Converter
	storage storage.ObjectStorage
	logger  *zap.Logger
	cfg     Config
	metrics *workerMetrics
}

var errDoclingWaiting = errors.New("waiting for docling")

type temporaryError interface{ Temporary() bool }

type statusCoder interface{ StatusCode() int }

func isTemporaryDoclingError(err error) (bool, int) {
	var tmp temporaryError
	if errors.As(err, &tmp) && tmp.Temporary() {
		var code statusCoder
		if errors.As(err, &code) {
			return true, code.StatusCode()
		}

		return true, 0
	}

	return false, 0
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
	m.doclingGauge, err = meter.Int64ObservableGauge("docling_transformer_queue_docling_waiting")
	if err != nil && logger != nil {
		logger.Warn("failed to create docling waiting gauge", zap.Error(err))
	}
	m.restoredCounter, err = meter.Int64Counter("docling_transformer_tasks_restored")
	if err != nil && logger != nil {
		logger.Warn("failed to create restored counter", zap.Error(err))
	}
	m.rejectedCounter, err = meter.Int64Counter("docling_transformer_tasks_rejected")
	if err != nil && logger != nil {
		logger.Warn("failed to create rejected counter", zap.Error(err))
	}
	m.temporaryRetry, err = meter.Int64Counter("docling_transformer_docling_temporary_retries")
	if err != nil && logger != nil {
		logger.Warn("failed to create temporary retry counter", zap.Error(err))
	}
	m.durationHist, err = meter.Float64Histogram("docling_transformer_processing_duration_ms")
	if err != nil && logger != nil {
		logger.Warn("failed to create duration histogram", zap.Error(err))
	}
	m.doclingWaitHist, err = meter.Float64Histogram("docling_transformer_docling_wait_duration_ms")
	if err != nil && logger != nil {
		logger.Warn("failed to create docling wait histogram", zap.Error(err))
	}
	m.doclingPolls, err = meter.Int64Histogram("docling_transformer_docling_poll_attempts")
	if err != nil && logger != nil {
		logger.Warn("failed to create docling polls histogram", zap.Error(err))
	}

	observables := make([]metric.Observable, 0, 3)
	if m.pendingGauge != nil {
		observables = append(observables, m.pendingGauge)
	}
	if m.processingGauge != nil {
		observables = append(observables, m.processingGauge)
	}
	if m.doclingGauge != nil {
		observables = append(observables, m.doclingGauge)
	}

	if len(observables) > 0 {
		if _, err := meter.RegisterCallback(func(ctx context.Context, observer metric.Observer) error {
			m.mu.Lock()
			defer m.mu.Unlock()
			if m.pendingGauge != nil {
				observer.ObserveInt64(m.pendingGauge, m.lastPending)
			}
			if m.processingGauge != nil {
				observer.ObserveInt64(m.processingGauge, m.lastProcessing)
			}
			if m.doclingGauge != nil {
				observer.ObserveInt64(m.doclingGauge, m.lastDocling)
			}
			return nil
		}, observables...); err != nil && logger != nil {
			logger.Warn("failed to register queue callback", zap.Error(err))
		}
	}

	return m
}

func (m *workerMetrics) recordQueueSizes(pending, processing, doclingWaiting int64) {
	if m == nil {
		return
	}

	m.mu.Lock()
	m.lastPending = pending
	m.lastProcessing = processing
	m.lastDocling = doclingWaiting
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

func (m *workerMetrics) recordDoclingWait(ctx context.Context, duration time.Duration) {
	if m == nil || m.doclingWaitHist == nil {
		return
	}

	m.doclingWaitHist.Record(ctx, float64(duration.Milliseconds()))
}

func (m *workerMetrics) recordDoclingPolls(ctx context.Context, attempts int) {
	if m == nil || m.doclingPolls == nil {
		return
	}

	m.doclingPolls.Record(ctx, int64(attempts))
}

func (m *workerMetrics) recordTemporaryRetry(ctx context.Context, retries int) {
	if m == nil || m.temporaryRetry == nil {
		return
	}

	m.temporaryRetry.Add(ctx, int64(retries))
}

func (p *Processor) Run(ctx context.Context) {
	p.runMaintenanceCycle(ctx)

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
			if errors.Is(err, errDoclingWaiting) {
				nextPoll := time.Time{}
				if task.NextPollAt != nil {
					nextPoll = *task.NextPollAt
				}
				p.logger.Info("docling pending", zap.String("queue_id", task.ID.String()), zap.String("correlation_id", correlationID), zap.Time("next_poll_at", nextPoll))
				continue
			}

			doclingWaiting := task.DoclingJobID != ""
			doclingLimit := errors.Is(err, converter.ErrDoclingPollingAttemptsExceeded)
			p.logger.Error("transformation failed", zap.String("queue_id", task.ID.String()), zap.String("correlation_id", correlationID), zap.Duration("duration", time.Since(start)), zap.Bool("docling_waiting", doclingWaiting), zap.Bool("docling_limit", doclingLimit), zap.String("docling_job_id", task.DoclingJobID), zap.Int("docling_poll_count", task.DoclingPollCount), zap.Error(err))
			if markErr := p.repo.MarkFailed(ctx, task.ID, buildFailureMessage(err, task)); markErr != nil {
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
	correlationID := telemetry.CorrelationIDFromContext(ctx)

	if task.MarkdownBucket == "" {
		task.MarkdownBucket = p.cfg.MarkdownBucket
	}
	if task.MarkdownBucket == "" {
		return fmt.Errorf("markdown bucket is empty")
	}

	if task.MarkdownObjectKey == "" {
		task.MarkdownObjectKey = transformations.BuildMarkdownKey(task.OriginalObjectKey)
	}

	jobID := task.DoclingJobID
	pollOffset := task.DoclingPollCount

	if jobID != "" {
		return p.pollDoclingJob(ctx, task, jobID, pollOffset)
	}

	fileExt := task.FilenameExtension
	if fileExt == "" {
		fileExt = transformations.NormalizeExtension(filepath.Ext(task.OriginalObjectKey))
	}

	contentType, err := converter.ResolveContentTypeByExtension(fileExt)
	if err != nil {
		return err
	}

	downloadCtx, cancelDownload := p.fastPhaseContext(ctx)
	defer cancelDownload()

	originalContent, err := p.storage.Download(downloadCtx, task.OriginalBucket, task.OriginalObjectKey)
	if err != nil {
		return fmt.Errorf("download original: %w", err)
	}

	originalFileName := filepath.Base(task.OriginalObjectKey)
	if filepath.Ext(originalFileName) == "" && fileExt != "" {
		originalFileName = fmt.Sprintf("document.%s", fileExt)
	}

	submitCtx, cancelSubmit := p.fastPhaseContext(ctx)
	defer cancelSubmit()

	jobID, err = p.conv.Submit(submitCtx, converter.DoclingConvertRequest{
		FileName:    originalFileName,
		Content:     originalContent,
		ContentType: contentType,
	})
	if err != nil {
		return fmt.Errorf("docling submit: %w", err)
	}

	nextPoll := p.nextPollTime(time.Now(), 0)
	if err := p.repo.MarkDoclingWaiting(ctx, task.ID, jobID, 0, nextPoll); err != nil {
		return fmt.Errorf("store docling job: %w", err)
	}

	task.DoclingJobID = jobID
	task.DoclingPollCount = 0
	task.NextPollAt = &nextPoll

	p.logger.Info("docling job submitted", zap.String("job_id", jobID), zap.String("correlation_id", correlationID), zap.Time("next_poll_at", nextPoll))

	return errDoclingWaiting
}

func (p *Processor) pollDoclingJob(ctx context.Context, task *repository.TransformationTask, jobID string, pollOffset int) error {
	if p.cfg.DoclingMaxAttempts > 0 && pollOffset >= p.cfg.DoclingMaxAttempts {
		return fmt.Errorf("%w: job %s has no remaining attempts after %d polls", converter.ErrDoclingPollingAttemptsExceeded, jobID, pollOffset)
	}

	statusResult, err := p.conv.CheckStatus(ctx, jobID)
	newPollCount := pollOffset + 1
	task.DoclingJobID = jobID
	task.DoclingPollCount = newPollCount

	if err := p.repo.UpdateDoclingInfo(ctx, task.ID, jobID, newPollCount); err != nil {
		return fmt.Errorf("store docling polling progress: %w", err)
	}

	if err != nil {
		p.metrics.recordDoclingPolls(ctx, 1)
		if isTemporary, statusCode := isTemporaryDoclingError(err); isTemporary {
			nextPoll := p.nextPollTime(time.Now(), newPollCount)
			task.DoclingPollCount = newPollCount
			task.NextPollAt = &nextPoll
			if markErr := p.repo.MarkDoclingWaiting(ctx, task.ID, jobID, newPollCount, nextPoll); markErr != nil {
				return fmt.Errorf("store docling waiting: %w", markErr)
			}

			p.metrics.recordTemporaryRetry(ctx, 1)
			p.logger.Warn("docling temporary error, will retry", zap.String("job_id", jobID), zap.Int("docling_polls", newPollCount), zap.Int("status_code", statusCode), zap.Time("next_poll_at", nextPoll), zap.Error(err))

			return errDoclingWaiting
		}

		p.logger.Error("docling status check failed", zap.String("job_id", jobID), zap.Int("docling_polls", newPollCount), zap.Error(err))

		if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
			nextPoll := p.nextPollTime(time.Now(), newPollCount)
			task.DoclingPollCount = newPollCount
			if markErr := p.repo.MarkDoclingWaiting(ctx, task.ID, jobID, newPollCount, nextPoll); markErr != nil {
				return fmt.Errorf("store docling waiting: %w", markErr)
			}

			task.NextPollAt = &nextPoll
			p.logger.Info("docling still processing", zap.String("job_id", jobID), zap.Time("next_poll_at", nextPoll), zap.Int("docling_polls", newPollCount))

			return errDoclingWaiting
		}

		return fmt.Errorf("docling status: %w", err)
	}

	p.metrics.recordDoclingPolls(ctx, 1)
	p.logger.Info("docling status checked", zap.String("job_id", jobID), zap.String("status", statusResult.Status), zap.Int("docling_polls", newPollCount))

	if statusResult.Result == nil {
		if p.cfg.DoclingMaxAttempts > 0 && newPollCount >= p.cfg.DoclingMaxAttempts {
			return fmt.Errorf("%w: job %s exceeded maximum polls (%d)", converter.ErrDoclingPollingAttemptsExceeded, jobID, newPollCount)
		}

		nextPoll := p.nextPollTime(time.Now(), newPollCount)
		if err := p.repo.MarkDoclingWaiting(ctx, task.ID, jobID, newPollCount, nextPoll); err != nil {
			return fmt.Errorf("store docling waiting: %w", err)
		}

		task.NextPollAt = &nextPoll
		p.logger.Info("docling still processing", zap.String("job_id", jobID), zap.Time("next_poll_at", nextPoll), zap.Int("docling_polls", newPollCount))

		return errDoclingWaiting
	}

	p.metrics.recordDoclingWait(ctx, time.Since(task.CreatedAt))

	result := *statusResult.Result
	mediaType := normalizeMediaType(result.MediaType, result.Charset)

	uploadCtx, cancelUpload := p.fastPhaseContext(ctx)
	defer cancelUpload()

	if err := p.storage.Upload(uploadCtx, task.MarkdownBucket, task.MarkdownObjectKey, mediaType, result.Content); err != nil {
		return fmt.Errorf("upload markdown: %w", err)
	}

	if err := p.repo.MarkDone(ctx, task.ID, task.MarkdownBucket, task.MarkdownObjectKey); err != nil {
		return fmt.Errorf("mark done: %w", err)
	}

	return nil
}

func normalizeMediaType(mediaType, charset string) string {
	mediaType = strings.TrimSpace(mediaType)
	if mediaType == "" {
		mediaType = "text/markdown"
	}

	lower := strings.ToLower(mediaType)
	if strings.Contains(lower, "charset=") {
		return mediaType
	}

	charset = strings.TrimSpace(charset)
	if charset == "" {
		charset = "utf-8"
	}

	return mediaType + "; charset=" + charset
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
	if pending, processing, doclingWaiting, err := p.repo.CountByStatus(ctx); err != nil {
		p.logger.Error("count tasks by status failed", zap.Error(err))
	} else {
		p.metrics.recordQueueSizes(int64(pending), int64(processing), int64(doclingWaiting))
		p.logger.Info("queue status snapshot", zap.Int("pending", pending), zap.Int("processing", processing), zap.Int("docling_waiting", doclingWaiting))
	}

	if ready, expired, err := p.repo.ActivateWaiting(ctx, time.Now(), p.cfg.DoclingMaxLifetime); err != nil {
		p.logger.Error("activate waiting tasks failed", zap.Error(err))
	} else if ready > 0 || expired > 0 {
		p.logger.Info("waiting tasks processed", zap.Int("activated", ready), zap.Int("expired", expired))
	}

	if p.cfg.ProcessingTimeout > 0 {
		restored, failed, err := p.repo.RestoreStuckProcessing(ctx, p.cfg.ProcessingTimeout, p.cfg.MaxProcessingRetries, p.cfg.DoclingMaxAttempts)
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

func (p *Processor) fastPhaseContext(ctx context.Context) (context.Context, context.CancelFunc) {
	if p.cfg.FastOperationTimeout <= 0 {
		return ctx, func() {}
	}

	return context.WithTimeout(ctx, p.cfg.FastOperationTimeout)
}

func (p *Processor) nextPollTime(now time.Time, pollCount int) time.Time {
	baseInterval := p.cfg.DoclingInitialInterval
	if baseInterval <= 0 {
		baseInterval = 15 * time.Second
	}

	backoff := p.cfg.DoclingBackoffFactor
	if backoff <= 1 {
		backoff = 2
	}

	maxInterval := p.cfg.DoclingMaxInterval
	if maxInterval <= 0 {
		maxInterval = 30 * time.Minute
	}

	interval := time.Duration(float64(baseInterval) * math.Pow(backoff, float64(pollCount)))
	if interval > maxInterval {
		interval = maxInterval
	}

	return now.Add(interval)
}

func (p *Processor) wait(ctx context.Context, interval time.Duration) {
	select {
	case <-ctx.Done():
	case <-time.After(interval):
	}
}

func buildFailureMessage(err error, task *repository.TransformationTask) string {
	if task == nil {
		return err.Error()
	}

	jobID := task.DoclingJobID
	pollCount := task.DoclingPollCount

	switch {
	case errors.Is(err, converter.ErrDoclingPollingAttemptsExceeded):
		return fmt.Sprintf("docling polling attempts exceeded for job %s after %d attempts", jobID, pollCount)
	case jobID != "":
		return fmt.Sprintf("docling job %s failed: %v", jobID, err)
	default:
		return err.Error()
	}
}
