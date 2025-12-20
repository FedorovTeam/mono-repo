package telemetry

import (
	"context"

	"github.com/google/uuid"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

type correlationContextKey struct{}

const CorrelationIDMetadataKey = "x-correlation-id"

func CorrelationIDFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	if value, ok := ctx.Value(correlationContextKey{}).(string); ok {
		return value
	}
	return ""
}

func ContextWithCorrelationID(ctx context.Context, correlationID string) context.Context {
	return context.WithValue(ctx, correlationContextKey{}, correlationID)
}

func EnsureCorrelationID(ctx context.Context) (context.Context, string) {
	if existing := CorrelationIDFromContext(ctx); existing != "" {
		return ctx, existing
	}
	newID := uuid.NewString()
	return ContextWithCorrelationID(ctx, newID), newID
}

func ExtractIncomingCorrelationID(ctx context.Context) (context.Context, string) {
	if md, ok := metadata.FromIncomingContext(ctx); ok {
		if values := md.Get(CorrelationIDMetadataKey); len(values) > 0 {
			return ContextWithCorrelationID(ctx, values[0]), values[0]
		}
	}
	return EnsureCorrelationID(ctx)
}

func InjectCorrelationID(ctx context.Context) context.Context {
	ctx, correlationID := EnsureCorrelationID(ctx)
	return WithOutgoingCorrelationID(ctx, correlationID)
}

func SetCorrelationHeader(ctx context.Context, correlationID string) {
	_ = grpc.SetHeader(ctx, metadata.Pairs(CorrelationIDMetadataKey, correlationID))
}

func WithOutgoingCorrelationID(ctx context.Context, correlationID string) context.Context {
	if correlationID == "" {
		return ctx
	}

	if existing := CorrelationIDFromContext(ctx); existing == "" {
		ctx = ContextWithCorrelationID(ctx, correlationID)
	}

	if md, ok := metadata.FromOutgoingContext(ctx); ok {
		md = md.Copy()
		md.Set(CorrelationIDMetadataKey, correlationID)
		return metadata.NewOutgoingContext(ctx, md)
	}

	md := metadata.New(map[string]string{CorrelationIDMetadataKey: correlationID})
	return metadata.NewOutgoingContext(ctx, md)
}
