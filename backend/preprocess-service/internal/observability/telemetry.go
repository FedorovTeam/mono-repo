package observability

import (
	"context"
	"errors"
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.opentelemetry.io/otel"
	otelprom "go.opentelemetry.io/otel/exporters/prometheus"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/propagation"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.12.0"
	"go.opentelemetry.io/otel/trace"
)

// Telemetry настраивает трассировку и метрики для сервиса.
type Telemetry struct {
	TracerProvider *sdktrace.TracerProvider
	MeterProvider  *sdkmetric.MeterProvider
	MetricsHandler http.Handler
}

// Setup инициализирует OpenTelemetry с экспортом метрик в формате Prometheus.
func Setup(ctx context.Context, serviceName string) (*Telemetry, error) {
	res, err := resource.New(ctx,
		resource.WithSchemaURL(semconv.SchemaURL),
		resource.WithAttributes(semconv.ServiceNameKey.String(serviceName)),
	)
	if err != nil {
		return nil, err
	}

	registry := prometheus.NewRegistry()
	promExporter, err := otelprom.New(
		otelprom.WithRegisterer(registry),
		otelprom.WithoutUnits(),
	)
	if err != nil {
		return nil, err
	}

	meterProvider := sdkmetric.NewMeterProvider(
		sdkmetric.WithReader(promExporter),
		sdkmetric.WithResource(res),
	)

	tracerProvider := sdktrace.NewTracerProvider(
		sdktrace.WithResource(res),
	)

	otel.SetTracerProvider(tracerProvider)
	otel.SetMeterProvider(meterProvider)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))

	return &Telemetry{
		TracerProvider: tracerProvider,
		MeterProvider:  meterProvider,
		MetricsHandler: promhttp.HandlerFor(registry, promhttp.HandlerOpts{}),
	}, nil
}

// Shutdown останавливает провайдеры телеметрии.
func (t *Telemetry) Shutdown(ctx context.Context) error {
	var shutdownErr error

	if t.MeterProvider != nil {
		shutdownErr = errors.Join(shutdownErr, t.MeterProvider.Shutdown(ctx))
	}

	if t.TracerProvider != nil {
		shutdownErr = errors.Join(shutdownErr, t.TracerProvider.Shutdown(ctx))
	}

	return shutdownErr
}

// Tracer возвращает tracer сервиса.
func (t *Telemetry) Tracer() trace.Tracer {
	if t == nil || t.TracerProvider == nil {
		return otel.Tracer("preprocess-service")
	}

	return t.TracerProvider.Tracer("preprocess-service")
}

// Meter возвращает meter сервиса.
func (t *Telemetry) Meter() metric.Meter {
	if t == nil || t.MeterProvider == nil {
		return otel.Meter("preprocess-service")
	}

	return t.MeterProvider.Meter("preprocess-service")
}
