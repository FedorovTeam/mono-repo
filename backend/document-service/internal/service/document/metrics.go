package document

import (
	"context"
	"sync"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/metric"
)

type workerMetrics struct {
	queueGauge   metric.Int64ObservableGauge
	batchHist    metric.Int64Histogram
	durationHist metric.Float64Histogram
	errorCount   metric.Int64Counter

	mu        sync.Mutex
	lastQueue int64
}

func newWorkerMetrics(name string) *workerMetrics {
	meter := otel.GetMeterProvider().Meter("document-service/" + name)
	m := &workerMetrics{}

	var err error
	m.queueGauge, err = meter.Int64ObservableGauge("document_service_" + name + "_queue_size")
	if err != nil {
		return m
	}

	if m.queueGauge != nil {
		if _, err := meter.RegisterCallback(func(ctx context.Context, observer metric.Observer) error {
			m.mu.Lock()
			defer m.mu.Unlock()
			observer.ObserveInt64(m.queueGauge, m.lastQueue)
			return nil
		}, m.queueGauge); err != nil {
			return m
		}
	}

	m.batchHist, _ = meter.Int64Histogram("document_service_" + name + "_batch_size")
	m.durationHist, _ = meter.Float64Histogram("document_service_" + name + "_duration_ms")
	m.errorCount, _ = meter.Int64Counter("document_service_" + name + "_errors_total")

	return m
}

func (m *workerMetrics) observeBatch(ctx context.Context, count int) {
	if m == nil {
		return
	}
	if m.batchHist != nil {
		m.batchHist.Record(ctx, int64(count))
	}
	m.mu.Lock()
	m.lastQueue = int64(count)
	m.mu.Unlock()
}

func (m *workerMetrics) recordDuration(ctx context.Context, duration time.Duration) {
	if m == nil || m.durationHist == nil {
		return
	}
	m.durationHist.Record(ctx, float64(duration.Milliseconds()))
}

func (m *workerMetrics) recordError(ctx context.Context) {
	if m == nil || m.errorCount == nil {
		return
	}
	m.errorCount.Add(ctx, 1)
}
