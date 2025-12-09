package server

import (
	"context"
	"fmt"
	"net"
	"path/filepath"
	"strings"

	"github.com/mono-repo/docling-transformer-service/internal/config"
	"github.com/mono-repo/docling-transformer-service/internal/converter"
	"github.com/mono-repo/docling-transformer-service/internal/storage"
	"github.com/mono-repo/docling-transformer-service/internal/telemetry"
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
	converter      converter.Converter
	storage        storage.ObjectStorage
	markdownBucket string
	logger         *zap.Logger
}

// NewTransformerAPI конструирует обработчик с переданным конвертером.
func NewTransformerAPI(conv converter.Converter, storage storage.ObjectStorage, markdownBucket string, logger *zap.Logger) *TransformerAPI {
	return &TransformerAPI{converter: conv, storage: storage, markdownBucket: markdownBucket, logger: logger}
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

	fileExt := strings.TrimPrefix(strings.ToLower(req.GetFilenameExtension()), ".")
	if fileExt == "" {
		fileExt = strings.TrimPrefix(strings.ToLower(filepath.Ext(req.GetOriginalObjectKey())), ".")
	}

	if fileExt == "" {
		return nil, status.Error(codes.InvalidArgument, "file extension is required")
	}

	supportedExt := map[string]string{
		"doc":  "application/msword",
		"docx": "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
		"pdf":  "application/pdf",
	}

	contentType, ok := supportedExt[fileExt]
	if !ok {
		return nil, status.Errorf(codes.InvalidArgument, "unsupported file extension: %s", fileExt)
	}

	originalContent, err := s.storage.Download(ctx, req.GetOriginalBucket(), req.GetOriginalObjectKey())
	if err != nil {
		s.logger.Error("download original failed", zap.String("correlation_id", correlationID), zap.Error(err))
		return nil, status.Errorf(codes.Internal, "download original: %v", err)
	}

	originalFileName := filepath.Base(req.GetOriginalObjectKey())
	if filepath.Ext(originalFileName) == "" && fileExt != "" {
		originalFileName = fmt.Sprintf("document.%s", fileExt)
	}

	result, err := s.converter.Convert(ctx, converter.DoclingConvertRequest{
		FileName:    originalFileName,
		Content:     originalContent,
		ContentType: contentType,
	})
	if err != nil {
		s.logger.Error("conversion failed", zap.String("correlation_id", correlationID), zap.Error(err))
		return nil, status.Errorf(codes.Internal, "conversion error: %v", err)
	}

	markdownKey := req.GetMarkdownObjectKey()
	if markdownKey == "" {
		markdownKey = buildMarkdownKey(req.GetOriginalObjectKey())
	}

	markdownContentType := result.MediaType
	if markdownContentType == "" {
		markdownContentType = "text/markdown"
	}

	if err := s.storage.Upload(ctx, markdownBucket, markdownKey, markdownContentType, result.Content); err != nil {
		s.logger.Error("upload markdown failed", zap.String("correlation_id", correlationID), zap.Error(err))
		return nil, status.Errorf(codes.Internal, "upload markdown: %v", err)
	}

	s.logger.Info("ConvertDocumentToMarkdown completed", zap.String("correlation_id", correlationID), zap.String("markdown_bucket", markdownBucket), zap.String("markdown_key", markdownKey))

	return &transformerv1.ConvertDocumentToMarkdownResponse{MarkdownContent: result.Content, MediaType: result.MediaType, MarkdownBucket: markdownBucket, MarkdownObjectKey: markdownKey}, nil
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
func Run(ctx context.Context, logger *zap.Logger, conv converter.Converter, storage storage.ObjectStorage, markdownBucket string, port int, obsCfg config.ObservabilityConfig) error {
	srv := newGRPCServer(logger, obsCfg)
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
