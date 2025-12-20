package server

import (
	"context"

	"github.com/mono-repo/table-extractor-service/internal/telemetry"
)

type CorrelationIDProvider interface {
	ExtractIncoming(ctx context.Context) (context.Context, string)
	Ensure(ctx context.Context) (context.Context, string)
	WithOutgoing(ctx context.Context, correlationID string) context.Context
	SetHeader(ctx context.Context, correlationID string)
	FromContext(ctx context.Context) string
}

type telemetryCorrelationIDProvider struct{}

func (telemetryCorrelationIDProvider) ExtractIncoming(ctx context.Context) (context.Context, string) {
	return telemetry.ExtractIncomingCorrelationID(ctx)
}

func (telemetryCorrelationIDProvider) Ensure(ctx context.Context) (context.Context, string) {
	return telemetry.EnsureCorrelationID(ctx)
}

func (telemetryCorrelationIDProvider) WithOutgoing(ctx context.Context, correlationID string) context.Context {
	if correlationID == "" {
		ctx, correlationID = telemetry.EnsureCorrelationID(ctx)
	} else {
		ctx = telemetry.ContextWithCorrelationID(ctx, correlationID)
	}
	return telemetry.WithOutgoingCorrelationID(ctx, correlationID)
}

func (telemetryCorrelationIDProvider) SetHeader(ctx context.Context, correlationID string) {
	telemetry.SetCorrelationHeader(ctx, correlationID)
}

func (telemetryCorrelationIDProvider) FromContext(ctx context.Context) string {
	return telemetry.CorrelationIDFromContext(ctx)
}

func defaultCorrelationIDProvider() CorrelationIDProvider {
	return telemetryCorrelationIDProvider{}
}
