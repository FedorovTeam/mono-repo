package observability

import (
	"backend-github-publisher-service/pkg/config"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"

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

type Providers struct {
	TracerProvider *sdktrace.TracerProvider
	MeterProvider  *sdkmetric.MeterProvider
	LogsEnabled    bool
}

func Setup(ctx context.Context, cfg config.ObservabilityConfig) (Providers, func(context.Context) error, error) {
	res, err := resource.New(ctx, resource.WithAttributes(attribute.String("service.name", cfg.ServiceName)))
	if err != nil {
		return Providers{}, nil, fmt.Errorf("failed to create resource: %w", err)
	}

	providers := Providers{}
	var shutdownFns []func(context.Context) error

	tracerProvider, tracerShutdown, err := initTracerProvider(ctx, cfg, res)
	if err != nil {
		return Providers{}, nil, err
	}
	providers.TracerProvider = tracerProvider
	shutdownFns = append(shutdownFns, tracerShutdown)

	meterProvider, meterShutdown, err := initMeterProvider(ctx, cfg, res)
	if err != nil {
		return Providers{}, nil, err
	}
	providers.MeterProvider = meterProvider
	shutdownFns = append(shutdownFns, meterShutdown)

	configureLogger(cfg.LogsEnabled)
	providers.LogsEnabled = cfg.LogsEnabled

	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(propagation.TraceContext{}, propagation.Baggage{}))

	shutdown := func(ctx context.Context) error {
		var shutdownErr error
		for _, fn := range shutdownFns {
			if err := fn(ctx); err != nil {
				if shutdownErr == nil {
					shutdownErr = err
				} else {
					shutdownErr = fmt.Errorf("%v; %w", shutdownErr, err)
				}
			}
		}
		return shutdownErr
	}

	return providers, shutdown, nil
}

func HTTPTransport(providers Providers, base http.RoundTripper) http.RoundTripper {
	if base == nil {
		base = http.DefaultTransport
	}

	return otelhttp.NewTransport(
		base,
		otelhttp.WithTracerProvider(providers.TracerProvider),
		otelhttp.WithMeterProvider(providers.MeterProvider),
		otelhttp.WithPropagators(otel.GetTextMapPropagator()),
	)
}

func GRPCServerOptions(providers Providers) []grpc.ServerOption {
	handler := otelgrpc.NewServerHandler(
		otelgrpc.WithTracerProvider(providers.TracerProvider),
		otelgrpc.WithMeterProvider(providers.MeterProvider),
		otelgrpc.WithPropagators(otel.GetTextMapPropagator()),
	)

	return []grpc.ServerOption{grpc.StatsHandler(handler)}
}

func initTracerProvider(ctx context.Context, cfg config.ObservabilityConfig, res *resource.Resource) (*sdktrace.TracerProvider, func(context.Context) error, error) {
	if !cfg.TracesEnabled {
		provider := sdktrace.NewTracerProvider(sdktrace.WithResource(res), sdktrace.WithSampler(sdktrace.NeverSample()))
		otel.SetTracerProvider(provider)
		return provider, provider.Shutdown, nil
	}

	if cfg.OTLPEndpoint == "" {
		return nil, nil, errors.New("observability.otlp_endpoint is required for traces")
	}

	exporter, err := otlptracegrpc.New(ctx, otlptracegrpc.WithEndpoint(cfg.OTLPEndpoint), otlptracegrpc.WithInsecure())
	if err != nil {
		return nil, nil, fmt.Errorf("failed to init trace exporter: %w", err)
	}

	provider := sdktrace.NewTracerProvider(
		sdktrace.WithResource(res),
		sdktrace.WithBatcher(exporter),
	)
	otel.SetTracerProvider(provider)

	return provider, provider.Shutdown, nil
}

func initMeterProvider(ctx context.Context, cfg config.ObservabilityConfig, res *resource.Resource) (*sdkmetric.MeterProvider, func(context.Context) error, error) {
	if !cfg.MetricsEnabled {
		provider := sdkmetric.NewMeterProvider(sdkmetric.WithResource(res))
		otel.SetMeterProvider(provider)
		return provider, provider.Shutdown, nil
	}

	if cfg.OTLPEndpoint == "" {
		return nil, nil, errors.New("observability.otlp_endpoint is required for metrics")
	}

	exporter, err := otlpmetricgrpc.New(ctx, otlpmetricgrpc.WithEndpoint(cfg.OTLPEndpoint), otlpmetricgrpc.WithInsecure())
	if err != nil {
		return nil, nil, fmt.Errorf("failed to init metric exporter: %w", err)
	}

	provider := sdkmetric.NewMeterProvider(
		sdkmetric.WithReader(sdkmetric.NewPeriodicReader(exporter)),
		sdkmetric.WithResource(res),
	)
	otel.SetMeterProvider(provider)

	return provider, provider.Shutdown, nil
}

func configureLogger(enabled bool) {
	if !enabled {
		log.SetOutput(io.Discard)
		return
	}

	log.SetOutput(os.Stdout)
}
