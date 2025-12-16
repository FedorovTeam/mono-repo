package transport

import (
    "context"
    "fmt"
    "net/http"
    "time"
)

type HTTPServer struct {
    addr   string
    server *http.Server
}

func NewHTTPServer(port int) *HTTPServer {
    mux := http.NewServeMux()
    mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
        w.WriteHeader(http.StatusOK)
        _, _ = w.Write([]byte("ok"))
    })

    return &HTTPServer{
        addr: fmt.Sprintf(":%d", port),
        server: &http.Server{
            Addr:              fmt.Sprintf(":%d", port),
            Handler:           mux,
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
