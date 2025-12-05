package server

import (
	"context"
	"fmt"
	"net"
	"path/filepath"
	"strings"

	"github.com/mono-repo/docling-transformer-service/internal/converter"
	"github.com/mono-repo/docling-transformer-service/internal/storage"
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
	converter      converter.Converter
	storage        storage.ObjectStorage
	markdownBucket string
	logger         *zap.Logger
}

// NewTransformerAPI конструирует обработчик с переданным конвертером.
func NewTransformerAPI(conv converter.Converter, storage storage.ObjectStorage, markdownBucket string, logger *zap.Logger) *TransformerAPI {
	return &TransformerAPI{converter: conv, storage: storage, markdownBucket: markdownBucket, logger: logger}
}

// ConvertDocxToMarkdown выполняет преобразование документа через конвертер.
func (s *TransformerAPI) ConvertDocxToMarkdown(ctx context.Context, req *transformerv1.ConvertDocxToMarkdownRequest) (*transformerv1.ConvertDocxToMarkdownResponse, error) {
	ctx, correlationID := telemetry.EnsureCorrelationID(ctx)

	s.logger.Info("ConvertDocxToMarkdown started", zap.String("correlation_id", correlationID),
		zap.String("document_id", req.GetDocumentId()),
		zap.String("original_bucket", req.GetOriginalBucket()),
		zap.String("original_object_key", req.GetOriginalObjectKey()))

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

	originalContent, err := s.storage.Download(ctx, req.GetOriginalBucket(), req.GetOriginalObjectKey())
	if err != nil {
		s.logger.Error("download original failed", zap.String("correlation_id", correlationID), zap.Error(err))
		return nil, status.Errorf(codes.Internal, "download original: %v", err)
	}

	result, err := s.converter.Convert(ctx, converter.DoclingConvertRequest{
		FileName: filepath.Base(req.GetOriginalObjectKey()),
		Content:  originalContent,
	})
	if err != nil {
		s.logger.Error("conversion failed", zap.String("correlation_id", correlationID), zap.Error(err))
		return nil, status.Errorf(codes.Internal, "conversion error: %v", err)
	}

	markdownKey := req.GetMarkdownObjectKey()
	if markdownKey == "" {
		markdownKey = buildMarkdownKey(req.GetOriginalObjectKey())
	}

	contentType := result.MediaType
	if contentType == "" {
		contentType = "text/markdown"
	}

	if err := s.storage.Upload(ctx, markdownBucket, markdownKey, contentType, result.Content); err != nil {
		s.logger.Error("upload markdown failed", zap.String("correlation_id", correlationID), zap.Error(err))
		return nil, status.Errorf(codes.Internal, "upload markdown: %v", err)
	}

	s.logger.Info("ConvertDocxToMarkdown completed", zap.String("correlation_id", correlationID), zap.String("markdown_bucket", markdownBucket), zap.String("markdown_key", markdownKey))

	return &transformerv1.ConvertDocxToMarkdownResponse{MarkdownContent: result.Content, MediaType: result.MediaType, MarkdownBucket: markdownBucket, MarkdownObjectKey: markdownKey}, nil
}

func buildMarkdownKey(originalKey string) string {
	ext := filepath.Ext(originalKey)
	base := strings.TrimSuffix(originalKey, ext)
	if base == "" {
		return originalKey + ".md"
	}

	return base + ".md"
}

// Run запускает gRPC сервер на указанном порте.
func Run(ctx context.Context, logger *zap.Logger, conv converter.Converter, storage storage.ObjectStorage, markdownBucket string, port int) error {
	srv := grpc.NewServer(grpc.UnaryInterceptor(LoggingInterceptor(logger)))
	transformerv1.RegisterTransformerServiceServer(srv, NewTransformerAPI(conv, storage, markdownBucket, logger))

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
