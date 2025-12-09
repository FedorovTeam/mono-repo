package server

import (
	"context"
	"fmt"
	"net"
	"path/filepath"

	"github.com/mono-repo/docling-transformer-service/internal/config"
	"github.com/mono-repo/docling-transformer-service/internal/converter"
	"github.com/mono-repo/docling-transformer-service/internal/repository"
	"github.com/mono-repo/docling-transformer-service/internal/telemetry"
	"github.com/mono-repo/docling-transformer-service/internal/transformations"
	transformerv1 "github.com/mono-repo/docling-transformer-service/proto/backend/transformer/v1"
	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

// TransformerAPI реализует gRPC-контракт TransformerService.
type TransformerAPI struct {
	transformerv1.UnimplementedTransformerServiceServer
	queueRepo      repository.QueueRepository
	markdownBucket string
	maxWorkers     int
	logger         *zap.Logger
}

// NewTransformerAPI конструирует обработчик с переданным конвертером.
func NewTransformerAPI(queueRepo repository.QueueRepository, markdownBucket string, maxWorkers int, logger *zap.Logger) *TransformerAPI {
	return &TransformerAPI{queueRepo: queueRepo, markdownBucket: markdownBucket, maxWorkers: maxWorkers, logger: logger}
}

// ConvertDocumentToMarkdown выполняет преобразование документа через конвертер.
func (s *TransformerAPI) ConvertDocumentToMarkdown(ctx context.Context, req *transformerv1.ConvertDocumentToMarkdownRequest) (*transformerv1.ConvertDocumentToMarkdownResponse, error) {
	ctx, correlationID := telemetry.EnsureCorrelationID(ctx)

	s.logger.Info("ConvertDocumentToMarkdown started", zap.String("correlation_id", correlationID),
		zap.String("document_id", req.GetDocumentId()),
		zap.String("original_bucket", req.GetOriginalBucket()),
		zap.String("original_object_key", req.GetOriginalObjectKey()),
		zap.String("filename_extension", req.GetFilenameExtension()))

	if req.GetDocumentId() == "" {
		return nil, status.Error(codes.InvalidArgument, "document_id is required")
	}
	if req.GetOriginalBucket() == "" || req.GetOriginalObjectKey() == "" {
		return nil, status.Error(codes.InvalidArgument, "original bucket and key are required")
	}

	markdownBucket := req.GetMarkdownBucket()
	if markdownBucket == "" {
		markdownBucket = s.markdownBucket
	}
	if markdownBucket == "" {
		return nil, status.Error(codes.InvalidArgument, "markdown bucket is required")
	}

	fileExt := transformations.NormalizeExtension(req.GetFilenameExtension())
	if fileExt == "" {
		fileExt = transformations.NormalizeExtension(filepath.Ext(req.GetOriginalObjectKey()))
	}
	if fileExt == "" {
		return nil, status.Error(codes.InvalidArgument, "file extension is required")
	}

	if _, err := converter.ResolveContentTypeByExtension(fileExt); err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "unsupported file extension: %s", fileExt)
	}

	markdownKey := req.GetMarkdownObjectKey()
	if markdownKey == "" {
		markdownKey = transformations.BuildMarkdownKey(req.GetOriginalObjectKey())
	}

	if s.maxWorkers > 0 {
		active, err := s.queueRepo.CountActive(ctx)
		if err != nil {
			s.logger.Error("count active tasks failed", zap.String("correlation_id", correlationID), zap.Error(err))
			return nil, status.Errorf(codes.Internal, "count active tasks: %v", err)
		}

		if active >= s.maxWorkers {
			return nil, status.Error(codes.ResourceExhausted, "no free worker slots")
		}
	}

	queueID, err := s.queueRepo.Enqueue(ctx, repository.EnqueueParams{
		DocumentID:        req.GetDocumentId(),
		OriginalBucket:    req.GetOriginalBucket(),
		OriginalObjectKey: req.GetOriginalObjectKey(),
		MarkdownBucket:    markdownBucket,
		MarkdownObjectKey: markdownKey,
		FilenameExtension: fileExt,
		CorrelationID:     correlationID,
	})
	if err != nil {
		s.logger.Error("enqueue transformation failed", zap.String("correlation_id", correlationID), zap.Error(err))
		return nil, status.Errorf(codes.Internal, "enqueue transformation: %v", err)
	}

	s.logger.Info("ConvertDocumentToMarkdown queued", zap.String("correlation_id", correlationID), zap.String("queue_id", queueID.String()))

	return &transformerv1.ConvertDocumentToMarkdownResponse{QueueId: queueID.String(), Status: string(repository.StatusPending), MarkdownBucket: markdownBucket, MarkdownObjectKey: markdownKey}, nil
}

// Run запускает gRPC сервер на указанном порте.
func Run(ctx context.Context, logger *zap.Logger, queueRepo repository.QueueRepository, markdownBucket string, maxWorkers int, port int, obsCfg config.ObservabilityConfig) error {
	srv := newGRPCServer(logger, obsCfg)
	transformerv1.RegisterTransformerServiceServer(srv, NewTransformerAPI(queueRepo, markdownBucket, maxWorkers, logger))

	listener, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}

	go func() {
		<-ctx.Done()
		srv.GracefulStop()
	}()

	logger.Info("gRPC server starting", zap.Int("port", port))
	if err := srv.Serve(listener); err != nil {
		return fmt.Errorf("serve: %w", err)
	}

	return nil
}

// LoggingInterceptor добавляет correlation-id в контекст и логирует каждый gRPC вызов.
func LoggingInterceptor(logger *zap.Logger) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
		ctx, correlationID := telemetry.EnsureCorrelationID(ctx)

		if md, ok := metadata.FromIncomingContext(ctx); ok {
			logger = logger.With(zap.Any("metadata", md))
		}

		logger.Info("incoming gRPC request", zap.String("method", info.FullMethod), zap.String("correlation_id", correlationID))
		resp, err := handler(ctx, req)
		if err != nil {
			logger.Error("gRPC request failed", zap.String("method", info.FullMethod), zap.String("correlation_id", correlationID), zap.Error(err))
			return resp, err
		}

		logger.Info("gRPC request completed", zap.String("method", info.FullMethod), zap.String("correlation_id", correlationID))
		return resp, nil
	}
}

func newGRPCServer(logger *zap.Logger, obsCfg config.ObservabilityConfig) *grpc.Server {
	interceptors := make([]grpc.UnaryServerInterceptor, 0, 1)
	opts := make([]grpc.ServerOption, 0, 2)

	if obsCfg.TracingEnabled || obsCfg.MetricsEnabled {
		otelOptions := telemetry.BuildOtelOptions(obsCfg)
		opts = append(opts, grpc.StatsHandler(otelgrpc.NewServerHandler(otelOptions...)))
	}

	if obsCfg.LoggingEnabled {
		interceptors = append(interceptors, LoggingInterceptor(logger))
	}

	if len(interceptors) > 0 {
		opts = append(opts, grpc.UnaryInterceptor(chainUnaryInterceptors(interceptors...)))
	}

	return grpc.NewServer(opts...)
}

func chainUnaryInterceptors(interceptors ...grpc.UnaryServerInterceptor) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
		h := handler
		for i := len(interceptors) - 1; i >= 0; i-- {
			next := h
			interceptor := interceptors[i]
			h = func(currentCtx context.Context, currentReq interface{}) (interface{}, error) {
				return interceptor(currentCtx, currentReq, info, next)
			}
		}
		return h(ctx, req)
	}
}
