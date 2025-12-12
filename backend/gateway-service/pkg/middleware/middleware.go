package middleware

import (
	"backend-gateway-service/pkg/ctxutils"
	"net/http"

	"github.com/google/uuid"
	"go.uber.org/zap"
)

// CorrelationIDMiddleware добавляет correlation-id в контекст и логи.
func CorrelationIDMiddleware(logger *zap.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			correlationID := r.Header.Get("X-Correlation-ID")
			if correlationID == "" {
				correlationID = uuid.New().String()
			}
			ctx := ctxutils.WithCorrelationID(r.Context(), correlationID)
			ctx = ctxutils.WithLogFields(ctx, map[string]string{"correlation_id": correlationID})
			r = r.WithContext(ctx)
			w.Header().Set("X-Correlation-ID", correlationID)
			logger := logger.With(zap.String("correlation_id", correlationID))
			logger.Info("incoming request", zap.String("path", r.URL.Path))
			next.ServeHTTP(w, r)
		})
	}
}

// APIKeyMiddleware проверяет значение заголовка X-API-Key.
func APIKeyMiddleware(expectedKey string, logger *zap.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if expectedKey == "" {
				http.Error(w, "api key is not configured", http.StatusInternalServerError)
				return
			}
			if r.Method == http.MethodOptions {
				next.ServeHTTP(w, r)
				return
			}
			apiKey := r.Header.Get("X-API-Key")
			if apiKey != expectedKey {
				logger.Warn("unauthorized request", zap.String("path", r.URL.Path))
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			ctx := ctxutils.WithAPIKey(r.Context(), apiKey)
			ctx = ctxutils.WithLogFields(ctx, map[string]string{"correlation_id": ctxutils.CorrelationID(ctx), "api_key": apiKey})
			r = r.WithContext(ctx)
			next.ServeHTTP(w, r)
		})
	}
}
