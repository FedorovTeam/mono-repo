package observability

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/propagation"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"google.golang.org/grpc"
)

// Providers содержит настроенные провайдеры метрик и трейсинга.
type Providers struct {
	TracerProvider *sdktrace.TracerProvider
	MeterProvider  *sdkmetric.MeterProvider
}

// Setup инициализирует OpenTelemetry провайдеры и возвращает функцию graceful shutdown.
func Setup(ctx context.Context, otlpEndpoint, serviceName string) (Providers, func(context.Context) error, error) {
	if otlpEndpoint == "" {
		return Providers{}, func(context.Context) error { return nil }, errors.New("otlp endpoint is required")
	}

	res, err := resource.New(ctx, resource.WithAttributes(attribute.String("service.name", serviceName)))
	if err != nil {
		return Providers{}, nil, fmt.Errorf("failed to create resource: %w", err)
	}

	traceExporter, err := otlptracegrpc.New(ctx, otlptracegrpc.WithEndpoint(otlpEndpoint), otlptracegrpc.WithInsecure())
	if err != nil {
		return Providers{}, nil, fmt.Errorf("failed to init trace exporter: %w", err)
	}

	metricExporter, err := otlpmetricgrpc.New(ctx, otlpmetricgrpc.WithEndpoint(otlpEndpoint), otlpmetricgrpc.WithInsecure())
	if err != nil {
		return Providers{}, nil, fmt.Errorf("failed to init metric exporter: %w", err)
	}

	tracerProvider := sdktrace.NewTracerProvider(
		sdktrace.WithResource(res),
		sdktrace.WithBatcher(traceExporter),
	)
	meterProvider := sdkmetric.NewMeterProvider(
		sdkmetric.WithReader(sdkmetric.NewPeriodicReader(metricExporter)),
		sdkmetric.WithResource(res),
	)

	otel.SetTracerProvider(tracerProvider)
	otel.SetMeterProvider(meterProvider)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(propagation.TraceContext{}, propagation.Baggage{}))

	shutdown := func(ctx context.Context) error {
		var shutdownErr error
		if err := tracerProvider.Shutdown(ctx); err != nil {
			shutdownErr = fmt.Errorf("trace shutdown: %w", err)
		}
		if err := meterProvider.Shutdown(ctx); err != nil {
			if shutdownErr != nil {
				shutdownErr = fmt.Errorf("%v; metric shutdown: %w", shutdownErr, err)
			} else {
				shutdownErr = fmt.Errorf("metric shutdown: %w", err)
			}
		}
		return shutdownErr
	}

	return Providers{TracerProvider: tracerProvider, MeterProvider: meterProvider}, shutdown, nil
}

// HTTPHandler wraps http handlers with otelhttp middleware.
func HTTPHandler(handlerName string, h http.Handler) http.Handler {
	return otelhttp.NewHandler(h, handlerName)
}

// GRPCDialOptions возвращает набор dial-опций для gRPC клиентов с interceptors.
func GRPCDialOptions(provider Providers) []grpc.DialOption {
	handler := otelgrpc.NewClientHandler(
		otelgrpc.WithTracerProvider(provider.TracerProvider),
		otelgrpc.WithMeterProvider(provider.MeterProvider),
	)
	return []grpc.DialOption{grpc.WithStatsHandler(handler)}
}
