package ctxutils

import (
	"context"

	"github.com/google/uuid"
)

type correlationKey struct{}

type correlationLogKey struct{}
type apiKey struct{}
type userKey struct{}

// User описывает извлечённые из токена данные.
type User struct {
	ID     string
	Email  string
	Groups []string
}

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

// EnsureCorrelationID возвращает контекст с установленным correlation-id и сам идентификатор.
func EnsureCorrelationID(ctx context.Context) (context.Context, string) {
	id := CorrelationID(ctx)
	if id == "" {
		id = uuid.New().String()
		ctx = WithCorrelationID(ctx, id)
	}
	return ctx, id
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

// WithAPIKey сохраняет API-ключ в контекст.
func WithAPIKey(ctx context.Context, key string) context.Context {
	return context.WithValue(ctx, apiKey{}, key)
}

// APIKey возвращает API-ключ из контекста, если он был передан.
func APIKey(ctx context.Context) string {
	if value, ok := ctx.Value(apiKey{}).(string); ok {
		return value
	}
	return ""
}

// WithUser сохраняет данные пользователя в контекст.
func WithUser(ctx context.Context, user User) context.Context {
	return context.WithValue(ctx, userKey{}, user)
}

// UserFromContext возвращает данные пользователя, если они есть.
func UserFromContext(ctx context.Context) *User {
	if value, ok := ctx.Value(userKey{}).(User); ok {
		return &value
	}
	return nil
}
