package telemetry

import (
	"context"

	"github.com/google/uuid"
	"google.golang.org/grpc/metadata"
)

type correlationKey struct{}

// EnsureCorrelationID извлекает correlation-id из контекста или метаданных gRPC и создаёт новый, если он отсутствует.
func EnsureCorrelationID(ctx context.Context) (context.Context, string) {
	if id, ok := ctx.Value(correlationKey{}).(string); ok && id != "" {
		return ctx, id
	}

	if md, ok := metadata.FromIncomingContext(ctx); ok {
		if values := md.Get("x-correlation-id"); len(values) > 0 && values[0] != "" {
			id := values[0]
			return context.WithValue(ctx, correlationKey{}, id), id
		}
	}

	id := uuid.NewString()
	return context.WithValue(ctx, correlationKey{}, id), id
}

// ContextWithCorrelationID добавляет переданный correlation-id в контекст без генерации нового значения.
func ContextWithCorrelationID(ctx context.Context, id string) (context.Context, string) {
	if id == "" {
		return EnsureCorrelationID(ctx)
	}

	return context.WithValue(ctx, correlationKey{}, id), id
}

// CorrelationIDFromContext возвращает correlation-id без генерации новых значений.
func CorrelationIDFromContext(ctx context.Context) string {
	if id, ok := ctx.Value(correlationKey{}).(string); ok {
		return id
	}

	if md, ok := metadata.FromIncomingContext(ctx); ok {
		if values := md.Get("x-correlation-id"); len(values) > 0 {
			return values[0]
		}
	}

	return ""
}
