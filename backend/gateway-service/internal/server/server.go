package server

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/99designs/gqlgen/graphql/handler"
	"github.com/99designs/gqlgen/graphql/playground"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"backend-gateway-service/graph"
	"backend-gateway-service/graph/generated"
	"backend-gateway-service/internal/clients/document"
	"backend-gateway-service/internal/clients/storage"
	"backend-gateway-service/internal/clients/temporal"
	"backend-gateway-service/internal/config"
	"backend-gateway-service/internal/logging"
	"backend-gateway-service/internal/middleware"
	"backend-gateway-service/internal/observability"
)

// Server отвечает за запуск HTTP GraphQL API.
type Server struct {
	cfg            config.Config
	logger         *zap.Logger
	documentClient document.Client
	temporalClient temporal.Client
	storageClient  storage.Client
	otelProviders  observability.Providers
	shutdownOTEL   func(context.Context) error
}

// New создаёт сервер и подключает клиентов.
func New(cfg config.Config) (*Server, error) {
	logger, err := logging.New(cfg.Logging.Level)
	if err != nil {
		return nil, fmt.Errorf("failed to init logger: %w", err)
	}

	providers, shutdown, err := observability.Setup(context.Background(), cfg.Observability.OTLPEndpoint, cfg.Observability.ServiceName)
	if err != nil {
		return nil, fmt.Errorf("failed to init telemetry: %w", err)
	}

	dialOpts := append(observability.GRPCDialOptions(providers), grpc.WithTransportCredentials(insecure.NewCredentials()))

	docClient, err := document.NewGRPCClient(cfg.DocumentService.Address, dialOpts...)
	if err != nil {
		return nil, fmt.Errorf("failed to init document client: %w", err)
	}

	storageClient, err := storage.NewGRPCClient(cfg.StorageService.Address, dialOpts...)
	if err != nil {
		return nil, fmt.Errorf("failed to init storage client: %w", err)
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
		storageClient:  storageClient,
		otelProviders:  providers,
		shutdownOTEL:   shutdown,
	}, nil
}

// Run запускает HTTP сервер.
func (s *Server) Run(ctx context.Context) error {
	defer s.documentClient.Close()
	defer s.temporalClient.Close()
	defer s.storageClient.Close()
	defer s.shutdownOTEL(context.Background())
	defer s.logger.Sync()

	gqlHandler := handler.NewDefaultServer(generated.NewExecutableSchema(generated.Config{Resolvers: &graph.Resolver{
		DocumentClient: s.documentClient,
		TemporalClient: s.temporalClient,
		StorageClient:  s.storageClient,
		Logger:         s.logger,
		UploadConfig: graph.UploadValidationConfig{
			MaxSizeBytes:        s.cfg.Uploads.MaxSizeBytes,
			AllowedExtensions:   s.cfg.Uploads.AllowedExtensions,
			SignedURLTTLSeconds: s.cfg.Uploads.SignedURLTTLSecond,
		},
	}}))

	instrumentedHandler := observability.HTTPHandler("graphql", gqlHandler)

	mux := http.NewServeMux()
	mux.Handle("/query", instrumentedHandler)
	mux.Handle("/playground", playground.Handler("GraphQL playground", "/query"))

	handlerWithMiddleware :=
		middleware.CorrelationIDMiddleware(s.logger)(
			middleware.APIKeyMiddleware(s.cfg.Security.APIKey, s.logger)(mux),
		)

	allowedOrigins := []string{
		"https://console.fedorov-team.tech",
		"http://localhost:3000", // на всякий случай для локальной разработки
	}
	corsHandler := middleware.CORSMiddleware(allowedOrigins)(handlerWithMiddleware)

	srv := &http.Server{
		Addr:              fmt.Sprintf(":%d", s.cfg.Server.Port),
		Handler:           corsHandler,
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
