package server

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/99designs/gqlgen/graphql/handler"
	"github.com/99designs/gqlgen/graphql/playground"
	"go.uber.org/zap"

	"backend-gateway-service/graph"
	"backend-gateway-service/graph/generated"
	"backend-gateway-service/internal/clients/document"
	"backend-gateway-service/internal/clients/temporal"
	"backend-gateway-service/internal/config"
	"backend-gateway-service/internal/logging"
	"backend-gateway-service/internal/middleware"
)

// Server отвечает за запуск HTTP GraphQL API.
type Server struct {
	cfg            config.Config
	logger         *zap.Logger
	documentClient document.Client
	temporalClient temporal.Client
}

// New создаёт сервер и подключает клиентов.
func New(cfg config.Config) (*Server, error) {
	logger, err := logging.New(cfg.Logging.Level)
	if err != nil {
		return nil, fmt.Errorf("failed to init logger: %w", err)
	}

	docClient, err := document.NewGRPCClient(cfg.DocumentService.Address)
	if err != nil {
		return nil, fmt.Errorf("failed to init document client: %w", err)
	}

	temporalClient, err := temporal.NewSDKClient(cfg.Temporal.Address, cfg.Temporal.Namespace, cfg.Temporal.TaskQueue)
	if err != nil {
		return nil, fmt.Errorf("failed to init temporal client: %w", err)
	}

	return &Server{
		cfg:            cfg,
		logger:         logger,
		documentClient: docClient,
		temporalClient: temporalClient,
	}, nil
}

// Run запускает HTTP сервер.
func (s *Server) Run(ctx context.Context) error {
	defer s.documentClient.Close()
	defer s.temporalClient.Close()
	defer s.logger.Sync()

	gqlHandler := handler.NewDefaultServer(generated.NewExecutableSchema(generated.Config{Resolvers: &graph.Resolver{
		DocumentClient: s.documentClient,
		TemporalClient: s.temporalClient,
		Logger:         s.logger,
	}}))

	mux := http.NewServeMux()
	mux.Handle("/query", gqlHandler)
	mux.Handle("/playground", playground.Handler("GraphQL playground", "/query"))

	handlerWithMiddleware := middleware.CorrelationIDMiddleware(s.logger)(middleware.APIKeyMiddleware(s.cfg.Security.APIKey, s.logger)(mux))

	srv := &http.Server{
		Addr:              fmt.Sprintf(":%d", s.cfg.Server.Port),
		Handler:           handlerWithMiddleware,
		ReadHeaderTimeout: 5 * time.Second,
	}

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}()

	s.logger.Info("starting graphql server", zap.Int("port", s.cfg.Server.Port))
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return err
	}
	return nil
}
