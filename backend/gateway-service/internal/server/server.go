package server

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/99designs/gqlgen/graphql"
	"github.com/99designs/gqlgen/graphql/handler"
	"github.com/99designs/gqlgen/graphql/playground"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"backend-gateway-service/graph"
	"backend-gateway-service/graph/generated"
	"backend-gateway-service/internal/clients/document"
	"backend-gateway-service/internal/clients/storage"
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

	providers, shutdown, err := observability.Setup(context.Background(), cfg.Observability.EnabledOrDefault(), cfg.Observability.OTLPEndpoint, cfg.Observability.ServiceName)
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

	return &Server{
		cfg:            cfg,
		logger:         logger,
		documentClient: docClient,
		storageClient:  storageClient,
		otelProviders:  providers,
		shutdownOTEL:   shutdown,
	}, nil
}

// Run запускает HTTP сервер.
func (s *Server) Run(ctx context.Context) error {
	defer s.documentClient.Close()
	defer s.storageClient.Close()
	defer s.shutdownOTEL(context.Background())
	defer s.logger.Sync()

	gqlHandler := handler.NewDefaultServer(generated.NewExecutableSchema(generated.Config{Resolvers: &graph.Resolver{
		DocumentClient: s.documentClient,
		StorageClient:  s.storageClient,
		Logger:         s.logger,
		UploadConfig: graph.UploadValidationConfig{
			MaxSizeBytes:        s.cfg.Uploads.MaxSizeBytes,
			AllowedExtensions:   s.cfg.Uploads.AllowedExtensions,
			SignedURLTTLSeconds: s.cfg.Uploads.SignedURLTTLSecond,
		},
	}}))
	gqlHandler.AroundOperations(traceGraphQLOperation)

	instrumentedHandler := observability.HTTPHandler("graphql", gqlHandler, s.otelProviders)

	webhookHandler := observability.HTTPHandler("storage-webhook", http.HandlerFunc(s.handleStorageNotification), s.otelProviders)

	gqlMux := http.NewServeMux()
	gqlMux.Handle("/query", instrumentedHandler)
	gqlMux.Handle("/playground", playground.Handler("GraphQL playground", "/query"))

	protectedGraphQL := middleware.APIKeyMiddleware(s.cfg.Security.APIKey, s.logger)(gqlMux)

	mux := http.NewServeMux()
	mux.Handle("/query", protectedGraphQL)
	mux.Handle("/playground", protectedGraphQL)
	mux.Handle("/internal/storage/uploaded", webhookHandler)

	handlerWithMiddleware := middleware.CorrelationIDMiddleware(s.logger)(mux)

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

func traceGraphQLOperation(ctx context.Context, next graphql.OperationHandler) graphql.ResponseHandler {
	opCtx := graphql.GetOperationContext(ctx)
	operationName := opCtx.OperationName
	if operationName == "" && opCtx.Operation != nil {
		operationName = string(opCtx.Operation.Operation)
	}

	tracer := otel.Tracer("gateway.graphql")
	ctx, span := tracer.Start(ctx, fmt.Sprintf("graphql %s", operationName))
	span.SetAttributes(
		attribute.String("graphql.operation.name", opCtx.OperationName),
		attribute.String("graphql.operation.type", string(opCtx.Operation.Operation)),
	)

	handler := next(ctx)
	return func(ctx context.Context) *graphql.Response {
		defer span.End()
		return handler(ctx)
	}
}
