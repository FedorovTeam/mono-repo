package transport

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel/metric"
)

type HTTPServer struct {
	addr   string
	server *http.Server
}

func NewHTTPServer(port int, db *pgxpool.Pool, metricsHandler http.Handler, meter metric.Meter, logger *slog.Logger) *HTTPServer {
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	mux.HandleFunc("/ready", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()

		if db != nil {
			if err := db.Ping(ctx); err != nil {
				logger.Error("readiness check failed", "error", err)
				http.Error(w, "database unavailable", http.StatusServiceUnavailable)
				return
			}
		}

		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ready"))
	})

	if metricsHandler != nil {
		mux.Handle("/metrics", metricsHandler)
	}

	handler := httpMiddleware(mux, meter, logger)
	wrapped := otelhttp.NewHandler(handler, "http-server")

	return &HTTPServer{
		addr: fmt.Sprintf(":%d", port),
		server: &http.Server{
			Addr:              fmt.Sprintf(":%d", port),
			Handler:           wrapped,
			ReadHeaderTimeout: 5 * time.Second,
		},
	}
}

func (s *HTTPServer) Start(ctx context.Context) error {
	errCh := make(chan error, 1)

	go func() {
		if err := s.server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errCh <- err
		}
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return s.server.Shutdown(shutdownCtx)
	}
}
