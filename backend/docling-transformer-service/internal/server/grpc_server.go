package server

import (
	"context"
	"fmt"
	"net"

	"github.com/mono-repo/docling-transformer-service/internal/converter"
	"github.com/mono-repo/docling-transformer-service/internal/telemetry"
	transformerv1 "github.com/mono-repo/docling-transformer-service/proto/backend/transformer/v1"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

// TransformerAPI реализует gRPC-контракт TransformerService.
type TransformerAPI struct {
	transformerv1.UnimplementedTransformerServiceServer
	converter converter.Converter
	logger    *zap.Logger
}

// NewTransformerAPI конструирует обработчик с переданным конвертером.
func NewTransformerAPI(conv converter.Converter, logger *zap.Logger) *TransformerAPI {
	return &TransformerAPI{converter: conv, logger: logger}
}

// ConvertDocxToMarkdown выполняет преобразование документа через конвертер.
func (s *TransformerAPI) ConvertDocxToMarkdown(ctx context.Context, req *transformerv1.ConvertDocxToMarkdownRequest) (*transformerv1.ConvertDocxToMarkdownResponse, error) {
        ctx, correlationID := telemetry.EnsureCorrelationID(ctx)

        s.logger.Info("ConvertDocxToMarkdown started", zap.String("correlation_id", correlationID))

        if len(req.GetContent()) == 0 {
                return nil, status.Error(codes.InvalidArgument, "content is required")
        }

        fileName := ""
        if ext := req.GetFilenameExtension(); ext != "" {
                fileName = fmt.Sprintf("document.%s", ext)
        }

        result, err := s.converter.Convert(ctx, converter.DoclingConvertRequest{
                FileName: fileName,
                Content:  req.GetContent(),
        })
        if err != nil {
                s.logger.Error("conversion failed", zap.String("correlation_id", correlationID), zap.Error(err))
                return nil, status.Errorf(codes.Internal, "conversion error: %v", err)
        }

        s.logger.Info("ConvertDocxToMarkdown completed", zap.String("correlation_id", correlationID))

        return &transformerv1.ConvertDocxToMarkdownResponse{MarkdownContent: result.Content, MediaType: result.MediaType}, nil
}

// Run запускает gRPC сервер на указанном порте.
func Run(ctx context.Context, logger *zap.Logger, conv converter.Converter, port int) error {
	srv := grpc.NewServer(grpc.UnaryInterceptor(LoggingInterceptor(logger)))
	transformerv1.RegisterTransformerServiceServer(srv, NewTransformerAPI(conv, logger))

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
