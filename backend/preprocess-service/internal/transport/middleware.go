package transport

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

const correlationIDHeader = "X-Correlation-ID"

type responseRecorder struct {
	http.ResponseWriter
	statusCode int
}

func (r *responseRecorder) WriteHeader(statusCode int) {
	r.statusCode = statusCode
	r.ResponseWriter.WriteHeader(statusCode)
}

func httpMiddleware(handler http.Handler, meter metric.Meter, logger *slog.Logger) http.Handler {
	requestCount, _ := meter.Int64Counter(
		"http_requests_total",
		metric.WithDescription("Количество HTTP запросов"),
	)

	requestDuration, _ := meter.Float64Histogram(
		"http_request_duration_ms",
		metric.WithDescription("Длительность HTTP запросов в миллисекундах"),
	)

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		corrID := r.Header.Get(correlationIDHeader)
		if corrID == "" {
			corrID = uuid.NewString()
		}

		r = r.WithContext(context.WithValue(r.Context(), correlationIDHeader, corrID))
		w.Header().Set(correlationIDHeader, corrID)

		recorder := &responseRecorder{ResponseWriter: w, statusCode: http.StatusOK}
		start := time.Now()
		handler.ServeHTTP(recorder, r)
		duration := time.Since(start)

		attrs := []attribute.KeyValue{
			attribute.String("http.method", r.Method),
			attribute.String("http.route", r.URL.Path),
			attribute.Int("http.status_code", recorder.statusCode),
		}

		requestCount.Add(r.Context(), 1, metric.WithAttributes(attrs...))
		requestDuration.Record(r.Context(), float64(duration.Milliseconds()), metric.WithAttributes(attrs...))

		recorder.Header().Set(correlationIDHeader, corrID)
		logger.Info("http request",
			"method", r.Method,
			"path", r.URL.Path,
			"status", recorder.statusCode,
			"duration", duration,
			"correlation_id", corrID,
		)
	})
}

func correlationIDFromContext(ctx context.Context) string {
	if val := ctx.Value(correlationIDHeader); val != nil {
		if id, ok := val.(string); ok {
			return id
		}
	}
	return ""
}

func correlationUnaryInterceptor(logger *slog.Logger) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
		md, ok := metadata.FromIncomingContext(ctx)
		if !ok {
			md = metadata.New(nil)
		}

		corrID := ""
		if values := md.Get("x-correlation-id"); len(values) > 0 {
			corrID = values[0]
		}
		if corrID == "" {
			corrID = uuid.NewString()
			md.Set("x-correlation-id", corrID)
		}

		ctx = metadata.NewIncomingContext(context.WithValue(ctx, correlationIDHeader, corrID), md)
		logger.Debug("attached correlation id", "correlation_id", corrID, "method", info.FullMethod)

		return handler(ctx, req)
	}
}

func metricsUnaryInterceptor(meter metric.Meter, logger *slog.Logger) grpc.UnaryServerInterceptor {
	requestCount, _ := meter.Int64Counter(
		"grpc_requests_total",
		metric.WithDescription("Количество gRPC вызовов"),
	)

	requestDuration, _ := meter.Float64Histogram(
		"grpc_request_duration_ms",
		metric.WithDescription("Длительность gRPC вызовов в миллисекундах"),
	)

	return func(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
		start := time.Now()
		resp, err := handler(ctx, req)
		code := status.Code(err)
		duration := time.Since(start)

		attrs := []attribute.KeyValue{
			attribute.String("rpc.system", "grpc"),
			attribute.String("rpc.method", info.FullMethod),
			attribute.String("rpc.grpc.status", code.String()),
		}

		requestCount.Add(ctx, 1, metric.WithAttributes(attrs...))
		requestDuration.Record(ctx, float64(duration.Milliseconds()), metric.WithAttributes(attrs...))

		logger.Info("grpc request",
			"method", info.FullMethod,
			"status", code.String(),
			"duration", duration,
			"correlation_id", correlationIDFromContext(ctx),
		)

		return resp, err
	}
}
