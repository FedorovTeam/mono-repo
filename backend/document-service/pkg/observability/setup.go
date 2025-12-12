package observability

import (
	"backend-document-service/pkg/config"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/metric/noop"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	tracesdk "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
	oteltrace "go.opentelemetry.io/otel/trace"
)

func SetupLogging(enabled bool) func() {
	if !enabled {
		log.SetOutput(io.Discard)
		return func() {}
	}

	log.SetFlags(log.LstdFlags | log.Lmicroseconds)
	return func() {}
}

func SetupOpenTelemetry(ctx context.Context, serviceName string, cfg config.Observability) (func(context.Context) error, error) {
	otel.SetTracerProvider(oteltrace.NewNoopTracerProvider())
	otel.SetMeterProvider(noop.NewMeterProvider())

	if !cfg.Traces.Enabled && !cfg.Metrics.Enabled {
		return func(context.Context) error { return nil }, nil
	}

	res, err := resource.New(ctx,
		resource.WithFromEnv(),
		resource.WithProcess(),
		resource.WithTelemetrySDK(),
		resource.WithAttributes(semconv.ServiceName(serviceName)),
	)
	if err != nil {
		return nil, fmt.Errorf("create resource: %w", err)
	}

	var traceProvider *tracesdk.TracerProvider
	var metricProvider *sdkmetric.MeterProvider

	if cfg.Traces.Enabled {
		traceExporter, err := otlptracegrpc.New(ctx, otlptracegrpc.WithInsecure())
		if err != nil {
			return nil, fmt.Errorf("init trace exporter: %w", err)
		}

		traceProvider = tracesdk.NewTracerProvider(
			tracesdk.WithBatcher(traceExporter),
			tracesdk.WithResource(res),
		)
		otel.SetTracerProvider(traceProvider)
	}

	if cfg.Metrics.Enabled {
		metricExporter, err := otlpmetricgrpc.New(ctx, otlpmetricgrpc.WithInsecure())
		if err != nil {
			return nil, fmt.Errorf("init metric exporter: %w", err)
		}

		reader := sdkmetric.NewPeriodicReader(metricExporter, sdkmetric.WithInterval(30*time.Second))
		metricProvider = sdkmetric.NewMeterProvider(
			sdkmetric.WithReader(reader),
			sdkmetric.WithResource(res),
		)
		otel.SetMeterProvider(metricProvider)
	}

	return func(ctx context.Context) error {
		var errs []error
		if traceProvider != nil {
			errs = append(errs, traceProvider.Shutdown(ctx))
		}
		if metricProvider != nil {
			errs = append(errs, metricProvider.Shutdown(ctx))
		}

		return errors.Join(errs...)
	}, nil
}
