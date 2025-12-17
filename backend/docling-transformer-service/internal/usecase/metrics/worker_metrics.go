package metrics

import (
	"context"
	"sync"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.uber.org/zap"
)

type WorkerMetrics struct {
	pendingGauge     metric.Int64ObservableGauge
	processingGauge  metric.Int64ObservableGauge
	doclingGauge     metric.Int64ObservableGauge
	restoredCounter  metric.Int64Counter
	rejectedCounter  metric.Int64Counter
	temporaryRetry   metric.Int64Counter
	retryAttempt     metric.Int64Counter
	failedRetry      metric.Int64Counter
	failedExhausted  metric.Int64Counter
	acquiredCounter  metric.Int64Counter
	doclingRequeued  metric.Int64Counter
	completedCounter metric.Int64Counter
	failedCounter    metric.Int64Counter
	durationHist     metric.Float64Histogram
	doclingWaitHist  metric.Float64Histogram
	doclingPolls     metric.Int64Histogram
	batchHist        metric.Int64Histogram
	doclingSubmit    metric.Int64Counter
	mu               sync.Mutex
	lastPending      int64
	lastProcessing   int64
	lastDocling      int64
}

func NewWorkerMetrics(logger *zap.Logger) *WorkerMetrics {
	meter := otel.GetMeterProvider().Meter("docling-transformer-service/worker")
	m := &WorkerMetrics{}

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
	m.retryAttempt, err = meter.Int64Counter("docling_transformer_tasks_retry_attempts")
	if err != nil && logger != nil {
		logger.Warn("failed to create retry attempt counter", zap.Error(err))
	}
	m.failedRetry, err = meter.Int64Counter("docling_transformer_tasks_failed_rescheduled")
	if err != nil && logger != nil {
		logger.Warn("failed to create failed retry counter", zap.Error(err))
	}
	m.failedExhausted, err = meter.Int64Counter("docling_transformer_tasks_failed_exhausted")
	if err != nil && logger != nil {
		logger.Warn("failed to create failed exhausted counter", zap.Error(err))
	}
	m.doclingRequeued, err = meter.Int64Counter("docling_transformer_docling_job_requeued")
	if err != nil && logger != nil {
		logger.Warn("failed to create docling requeued counter", zap.Error(err))
	}
	m.acquiredCounter, err = meter.Int64Counter("docling_transformer_tasks_acquired")
	if err != nil && logger != nil {
		logger.Warn("failed to create acquired counter", zap.Error(err))
	}
	m.completedCounter, err = meter.Int64Counter("docling_transformer_tasks_completed")
	if err != nil && logger != nil {
		logger.Warn("failed to create completed counter", zap.Error(err))
	}
	m.failedCounter, err = meter.Int64Counter("docling_transformer_tasks_failed")
	if err != nil && logger != nil {
		logger.Warn("failed to create failed counter", zap.Error(err))
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
	m.batchHist, err = meter.Int64Histogram("docling_transformer_queue_batch_size")
	if err != nil && logger != nil {
		logger.Warn("failed to create batch size histogram", zap.Error(err))
	}
	m.doclingSubmit, err = meter.Int64Counter("docling_transformer_docling_submitted")
	if err != nil && logger != nil {
		logger.Warn("failed to create docling submit counter", zap.Error(err))
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

func (m *WorkerMetrics) RecordQueueSizes(pending, processing, doclingWaiting int64) {
	if m == nil {
		return
	}

	m.mu.Lock()
	m.lastPending = pending
	m.lastProcessing = processing
	m.lastDocling = doclingWaiting
	m.mu.Unlock()
}

func (m *WorkerMetrics) RecordRecoveries(ctx context.Context, restored, failed int) {
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

func (m *WorkerMetrics) RecordAcquired(ctx context.Context, count int) {
	if m == nil || count <= 0 {
		return
	}

	if m.acquiredCounter != nil {
		m.acquiredCounter.Add(ctx, int64(count))
	}

	if m.batchHist != nil {
		m.batchHist.Record(ctx, int64(count))
	}
}

func (m *WorkerMetrics) RecordOutcome(ctx context.Context, success bool) {
	if m == nil {
		return
	}

	switch {
	case success && m.completedCounter != nil:
		m.completedCounter.Add(ctx, 1)
	case !success && m.failedCounter != nil:
		m.failedCounter.Add(ctx, 1)
	}
}

func (m *WorkerMetrics) RecordDoclingSubmit(ctx context.Context, forceOCR bool, ocrLang, pdfBackend string) {
	if m == nil || m.doclingSubmit == nil {
		return
	}

	attrs := []attribute.KeyValue{attribute.Bool("force_ocr", forceOCR)}
	if ocrLang != "" {
		attrs = append(attrs, attribute.String("ocr_lang", ocrLang))
	}
	if pdfBackend != "" {
		attrs = append(attrs, attribute.String("pdf_backend", pdfBackend))
	}

	m.doclingSubmit.Add(ctx, 1, metric.WithAttributes(attrs...))
}

func (m *WorkerMetrics) RecordDuration(ctx context.Context, durationMs float64) {
	if m == nil || m.durationHist == nil {
		return
	}

	m.durationHist.Record(ctx, durationMs)
}

func (m *WorkerMetrics) RecordDoclingWait(ctx context.Context, durationMs float64) {
	if m == nil || m.doclingWaitHist == nil {
		return
	}

	m.doclingWaitHist.Record(ctx, durationMs)
}

func (m *WorkerMetrics) RecordDoclingPolls(ctx context.Context, attempts int) {
	if m == nil || m.doclingPolls == nil {
		return
	}

	m.doclingPolls.Record(ctx, int64(attempts))
}

func (m *WorkerMetrics) RecordTemporaryRetry(ctx context.Context, retries int) {
	if m == nil || m.temporaryRetry == nil {
		return
	}

	m.temporaryRetry.Add(ctx, int64(retries))
}

func (m *WorkerMetrics) RecordRetryAttempt(ctx context.Context, attempt int) {
	if m == nil || m.retryAttempt == nil || attempt <= 1 {
		return
	}

	m.retryAttempt.Add(ctx, 1)
}

func (m *WorkerMetrics) RecordFailedRetries(ctx context.Context, retried, exhausted int) {
	if m == nil {
		return
	}

	if retried > 0 && m.failedRetry != nil {
		m.failedRetry.Add(ctx, int64(retried))
	}

	if exhausted > 0 && m.failedExhausted != nil {
		m.failedExhausted.Add(ctx, int64(exhausted))
	}
}

func (m *WorkerMetrics) RecordDoclingRequeued(ctx context.Context) {
	if m == nil || m.doclingRequeued == nil {
		return
	}

	m.doclingRequeued.Add(ctx, 1)
}
