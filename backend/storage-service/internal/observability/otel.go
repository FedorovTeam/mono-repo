package observability

import (
	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"go.opentelemetry.io/otel"
	otellog "go.opentelemetry.io/otel/log/global"
	lognoop "go.opentelemetry.io/otel/log/noop"
	metricnoop "go.opentelemetry.io/otel/metric/noop"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc"
)

// Config управляет включением экспорта телеметрии.
type Config struct {
	EnableMetrics bool
	EnableTraces  bool
	EnableLogs    bool
}

// ConfigureProviders настраивает no-op провайдеры для отключенных каналов наблюдаемости.
func ConfigureProviders(cfg Config) {
	if !cfg.EnableTraces {
		otel.SetTracerProvider(trace.NewNoopTracerProvider())
	}
	if !cfg.EnableMetrics {
		otel.SetMeterProvider(metricnoop.NewMeterProvider())
	}
	if !cfg.EnableLogs {
		otellog.SetLoggerProvider(lognoop.NewLoggerProvider())
	}

	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))
}

// GRPCServerOptions возвращает опции сервера для включения OTEL-интерцепторов.
func GRPCServerOptions(cfg Config, additionalInterceptors ...grpc.UnaryServerInterceptor) []grpc.ServerOption {
	interceptors := append([]grpc.UnaryServerInterceptor{}, additionalInterceptors...)
	opts := make([]grpc.ServerOption, 0, 2)

	if cfg.EnableMetrics || cfg.EnableTraces {
		handler := otelgrpc.NewServerHandler(
			otelgrpc.WithTracerProvider(otel.GetTracerProvider()),
			otelgrpc.WithMeterProvider(otel.GetMeterProvider()),
			otelgrpc.WithPropagators(otel.GetTextMapPropagator()),
		)
		opts = append(opts, grpc.StatsHandler(handler))

		interceptors = append([]grpc.UnaryServerInterceptor{
			otelgrpc.UnaryServerInterceptor(
				otelgrpc.WithTracerProvider(otel.GetTracerProvider()),
				otelgrpc.WithMeterProvider(otel.GetMeterProvider()),
				otelgrpc.WithPropagators(otel.GetTextMapPropagator()),
			),
		}, interceptors...)
	}

	if len(interceptors) > 0 {
		opts = append(opts, grpc.ChainUnaryInterceptor(interceptors...))
	}

	return opts
}
