package telemetry

import (
	"context"
	"fmt"
	"time"

	"github.com/mono-repo/docling-transformer-service/internal/config"
	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	metricnoop "go.opentelemetry.io/otel/metric/noop"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	"go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.24.0"
	nooptrace "go.opentelemetry.io/otel/trace/noop"
	"go.uber.org/zap"
)

// NewLogger возвращает production-логгер при включённом флаге или no-op реализацию.
func NewLogger(enabled bool) (*zap.Logger, error) {
	if !enabled {
		return zap.NewNop(), nil
	}

	return zap.NewProduction()
}

// SetupProviders инициализирует OTEL tracer/meter provider и возвращает функцию корректного завершения.
// При выключенных флагах устанавливаются no-op провайдеры и возвращается no-op shutdown.
func SetupProviders(ctx context.Context, cfg config.ObservabilityConfig, serviceName string, logger *zap.Logger) (func(context.Context) error, error) {
	shutdown := func(context.Context) error { return nil }

	if !cfg.TracingEnabled && !cfg.MetricsEnabled {
		otel.SetTracerProvider(nooptrace.NewTracerProvider())
		otel.SetMeterProvider(metricnoop.NewMeterProvider())
		otel.SetTextMapPropagator(propagation.TraceContext{})
		return shutdown, nil
	}

	res, err := resource.New(ctx, resource.WithAttributes(semconv.ServiceName(serviceName)))
	if err != nil {
		return nil, fmt.Errorf("build resource: %w", err)
	}

	shutdownFns := make([]func(context.Context) error, 0, 2)

	if cfg.TracingEnabled {
		exporter, err := otlptracegrpc.New(ctx)
		if err != nil {
			return nil, fmt.Errorf("create trace exporter: %w", err)
		}

		tp := trace.NewTracerProvider(
			trace.WithBatcher(exporter),
			trace.WithResource(res),
		)
		otel.SetTracerProvider(tp)
		shutdownFns = append(shutdownFns, tp.Shutdown)
	} else {
		otel.SetTracerProvider(nooptrace.NewTracerProvider())
	}

	if cfg.MetricsEnabled {
		exporter, err := otlpmetricgrpc.New(ctx)
		if err != nil {
			return nil, fmt.Errorf("create metric exporter: %w", err)
		}

		reader := metric.NewPeriodicReader(exporter)
		meterProvider := metric.NewMeterProvider(
			metric.WithReader(reader),
			metric.WithResource(res),
		)
		otel.SetMeterProvider(meterProvider)
		shutdownFns = append(shutdownFns, meterProvider.Shutdown)
	} else {
		otel.SetMeterProvider(metricnoop.NewMeterProvider())
	}

	otel.SetTextMapPropagator(propagation.TraceContext{})

	shutdown = func(ctx context.Context) error {
		var shutdownErr error
		for i := len(shutdownFns) - 1; i >= 0; i-- {
			if err := shutdownFns[i](ctx); err != nil {
				shutdownErr = err
				if logger != nil {
					logger.Error("failed to shutdown observability provider", zap.Error(err))
				}
			}
		}
		return shutdownErr
	}

	return shutdown, nil
}

// BuildOtelOptions формирует опции gRPC telemetry handler'а в зависимости от флагов.
func BuildOtelOptions(cfg config.ObservabilityConfig) []otelgrpc.Option {
	options := make([]otelgrpc.Option, 0, 2)
	if cfg.TracingEnabled {
		options = append(options, otelgrpc.WithTracerProvider(otel.GetTracerProvider()))
	}
	if cfg.MetricsEnabled {
		options = append(options, otelgrpc.WithMeterProvider(otel.GetMeterProvider()))
	}

	return options
}

// ShutdownWithTimeout вызывает переданный shutdown с таймаутом и пишет ошибку в логгер.
func ShutdownWithTimeout(ctx context.Context, timeout time.Duration, logger *zap.Logger, shutdown func(context.Context) error) {
	if shutdown == nil {
		return
	}

	shutdownCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	if err := shutdown(shutdownCtx); err != nil && logger != nil {
		logger.Error("failed to shutdown observability", zap.Error(err))
	}
}
