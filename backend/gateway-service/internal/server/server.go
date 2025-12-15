package server

import (
	"backend-gateway-service/pkg/middleware"
	"backend-gateway-service/pkg/observability"
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/99designs/gqlgen/graphql"
	"github.com/99designs/gqlgen/graphql/handler"
	"github.com/99designs/gqlgen/graphql/playground"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.uber.org/zap"

	"backend-gateway-service/graph"
	"backend-gateway-service/graph/generated"
	"backend-gateway-service/internal/config"
	"backend-gateway-service/internal/domain"
	"backend-gateway-service/internal/infrastructure/webhook"
	"backend-gateway-service/internal/usecase"
)

// Server отвечает за запуск HTTP GraphQL API.
type Server struct {
	cfg                  config.Config
	logger               *zap.Logger
	documentClient       domain.DocumentClient
	storageClient        domain.StorageClient
	uploadUseCase        usecase.DocumentUploader
	docUseCase           usecase.DocumentProvider
	uploadConfirmationUC usecase.UploadConfirmationHandler
	otelProviders        observability.Providers
	shutdownOTEL         func(context.Context) error
}

// Dependencies содержит внешние зависимости сервера.
type Dependencies struct {
	Logger         *zap.Logger
	DocumentClient domain.DocumentClient
	StorageClient  domain.StorageClient
	Providers      observability.Providers
	ShutdownOTEL   func(context.Context) error
}

// New создаёт сервер из готовых зависимостей.
func New(cfg config.Config, deps Dependencies) (*Server, error) {
	if deps.Logger == nil {
		return nil, errors.New("logger is required")
	}
	if deps.DocumentClient == nil {
		return nil, errors.New("document client is required")
	}
	if deps.StorageClient == nil {
		return nil, errors.New("storage client is required")
	}
	if deps.ShutdownOTEL == nil {
		deps.ShutdownOTEL = func(context.Context) error { return nil }
	}

	uploadUseCase := usecase.NewUploadService(deps.DocumentClient, deps.StorageClient, usecase.UploadConfig{
		SignedURLTTLSeconds: cfg.Uploads.SignedURLTTLSecond,
	})
	docUseCase := usecase.NewDocumentService(deps.DocumentClient)
	webhookParser := webhook.NewMinIOWebhookParser()
	uploadConfirmationUseCase := usecase.NewUploadConfirmationService(webhookParser, deps.DocumentClient)

	return &Server{
		cfg:                  cfg,
		logger:               deps.Logger,
		documentClient:       deps.DocumentClient,
		storageClient:        deps.StorageClient,
		uploadUseCase:        uploadUseCase,
		docUseCase:           docUseCase,
		uploadConfirmationUC: uploadConfirmationUseCase,
		otelProviders:        deps.Providers,
		shutdownOTEL:         deps.ShutdownOTEL,
	}, nil
}

// Run запускает HTTP сервер.
func (s *Server) Run(ctx context.Context) error {
	defer s.documentClient.Close()
	defer s.storageClient.Close()
	defer s.shutdownOTEL(context.Background())
	defer s.logger.Sync()

	gqlHandler := handler.NewDefaultServer(generated.NewExecutableSchema(generated.Config{Resolvers: &graph.Resolver{
		DocumentUploader: s.uploadUseCase,
		DocumentService:  s.docUseCase,
		Logger:           s.logger,
		UploadConfig: graph.UploadValidationConfig{
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
