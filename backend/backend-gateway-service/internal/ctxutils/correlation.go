package ctxutils

import (
	"context"
)

type correlationKey struct{}

type correlationLogKey struct{}

// WithCorrelationID сохраняет correlation-id в контексте.
func WithCorrelationID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, correlationKey{}, id)
}

// CorrelationID извлекает correlation-id из контекста.
func CorrelationID(ctx context.Context) string {
	if value, ok := ctx.Value(correlationKey{}).(string); ok {
		return value
	}
	return ""
}

// WithLogFields добавляет строковое представление логовых полей в контекст.
func WithLogFields(ctx context.Context, fields map[string]string) context.Context {
	return context.WithValue(ctx, correlationLogKey{}, fields)
}

// LogFields возвращает сохранённые логовые поля.
func LogFields(ctx context.Context) map[string]string {
	if value, ok := ctx.Value(correlationLogKey{}).(map[string]string); ok {
		return value
	}
	return map[string]string{}
}
