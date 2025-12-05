package document

import (
	"context"

	"github.com/google/uuid"
	commonpb "go.temporal.io/api/common/v1"
	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/workflow"
	"google.golang.org/grpc/metadata"
)

type correlationIDContextKey struct{}

// ContextWithCorrelationID сохраняет correlation-id в контексте.
func ContextWithCorrelationID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, correlationIDContextKey{}, id)
}

// CorrelationIDFromContext возвращает correlation-id из контекста или gRPC-метаданных.
func CorrelationIDFromContext(ctx context.Context) string {
	if val, ok := ctx.Value(correlationIDContextKey{}).(string); ok && val != "" {
		return val
	}
	if md, ok := metadata.FromIncomingContext(ctx); ok {
		if vals := md.Get(CorrelationIDHeader); len(vals) > 0 {
			return vals[0]
		}
	}
	return ""
}

// EnsureCorrelationID гарантирует наличие correlation-id, генерируя его при отсутствии.
func EnsureCorrelationID(ctx context.Context) (context.Context, string) {
	if id := CorrelationIDFromContext(ctx); id != "" {
		return ContextWithCorrelationID(ctx, id), id
	}
	generated := uuid.NewString()
	return ContextWithCorrelationID(ctx, generated), generated
}

// CorrelationIDFromWorkflowContext извлекает correlation-id из контекста workflow.
func CorrelationIDFromWorkflowContext(ctx workflow.Context) string {
	if val := ctx.Value(correlationIDContextKey{}); val != nil {
		if id, ok := val.(string); ok {
			return id
		}
	}
	return ""
}

// WithWorkflowCorrelationID добавляет correlation-id в контекст workflow.
func WithWorkflowCorrelationID(ctx workflow.Context, id string) workflow.Context {
	return workflow.WithValue(ctx, correlationIDContextKey{}, id)
}

// CorrelationIDPropagator прокидывает correlation-id между клиентом, workflow и активити.
type CorrelationIDPropagator struct{}

func NewCorrelationIDPropagator() *CorrelationIDPropagator {
	return &CorrelationIDPropagator{}
}

func (p *CorrelationIDPropagator) Inject(ctx context.Context, writer workflow.HeaderWriter) error {
	if id := CorrelationIDFromContext(ctx); id != "" {
		p.setHeader(writer, id)
	}
	return nil
}

func (p *CorrelationIDPropagator) InjectFromWorkflow(ctx workflow.Context, writer workflow.HeaderWriter) error {
	if id := CorrelationIDFromWorkflowContext(ctx); id != "" {
		p.setHeader(writer, id)
	}
	return nil
}

func (p *CorrelationIDPropagator) Extract(ctx context.Context, reader workflow.HeaderReader) (context.Context, error) {
	if val, ok := reader.Get(CorrelationIDHeader); ok {
		if id := decodeCorrelationID(val); id != "" {
			ctx = ContextWithCorrelationID(ctx, id)
		}
	}
	return ctx, nil
}

func (p *CorrelationIDPropagator) ExtractToWorkflow(ctx workflow.Context, reader workflow.HeaderReader) (workflow.Context, error) {
	if val, ok := reader.Get(CorrelationIDHeader); ok {
		if id := decodeCorrelationID(val); id != "" {
			ctx = WithWorkflowCorrelationID(ctx, id)
		}
	}
	return ctx, nil
}

func (p *CorrelationIDPropagator) setHeader(writer workflow.HeaderWriter, id string) {
	payload, err := converter.GetDefaultDataConverter().ToPayload(id)
	if err != nil {
		return
	}
	writer.Set(CorrelationIDHeader, payload)
}

func decodeCorrelationID(payload *commonpb.Payload) string {
	var id string
	if err := converter.GetDefaultDataConverter().FromPayload(payload, &id); err == nil {
		return id
	}
	var raw []byte
	if err := converter.GetDefaultDataConverter().FromPayload(payload, &raw); err == nil {
		return string(raw)
	}
	return ""
}
