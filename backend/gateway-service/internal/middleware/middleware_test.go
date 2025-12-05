package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"backend-gateway-service/internal/ctxutils"
	"go.uber.org/zap"
)

func TestAPIKeyMiddleware(t *testing.T) {
	logger := zap.NewNop()
	handler := APIKeyMiddleware("secret", logger)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/", nil))
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected unauthorized, got %d", rr.Code)
	}

	rr = httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-API-Key", "secret")
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
}

func TestCorrelationIDMiddleware(t *testing.T) {
	logger := zap.NewNop()
	var captured string

	handler := CorrelationIDMiddleware(logger)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured = ctxutils.CorrelationID(r.Context())
		w.WriteHeader(http.StatusNoContent)
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Correlation-ID", "corr-123")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if captured != "corr-123" {
		t.Fatalf("expected correlation id to propagate, got %s", captured)
	}
	if rr.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", rr.Code)
	}
}
