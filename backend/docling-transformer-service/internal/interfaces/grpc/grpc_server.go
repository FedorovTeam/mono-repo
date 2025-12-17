// backend/docling-transformer-service/internal/interfaces/grpc/grpc_server.go
package grpcapi

import (
	"context"
	"errors"
	"fmt"
	"net"

	"github.com/google/uuid"
	"github.com/mono-repo/docling-transformer-service/internal/domain"
	transformations "github.com/mono-repo/docling-transformer-service/internal/domain/transformations"
	"github.com/mono-repo/docling-transformer-service/internal/infrastructure/telemetry"
	"github.com/mono-repo/docling-transformer-service/internal/usecase"
	transformerv1 "github.com/mono-repo/docling-transformer-service/proto/backend/transformer/v1"
	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"go.opentelemetry.io/otel"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type TransformerUseCase interface {
	SubmitConversion(ctx context.Context, params usecase.SubmitConversionRequest) (usecase.SubmitConversionResult, error)
	WaitForResult(ctx context.Context, queueID uuid.UUID) (*transformations.TransformationTask, error)
	ConvertDocxContent(ctx context.Context, filename string, content []byte) (domain.ConvertResult, error)
	FetchContentByURL(ctx context.Context, rawURL string) ([]byte, error)
}

type TransformerAPI struct {
	transformerv1.UnimplementedTransformerServiceServer
	usecase TransformerUseCase
	logger  *zap.Logger
}

func NewTransformerAPI(uc TransformerUseCase, logger *zap.Logger) *TransformerAPI {
	if logger == nil {
		logger = zap.NewNop()
	}

	return &TransformerAPI{usecase: uc, logger: logger}
}

func (s *TransformerAPI) ConvertDocumentToMarkdown(ctx context.Context, req *transformerv1.ConvertDocumentToMarkdownRequest) (*transformerv1.ConvertDocumentToMarkdownResponse, error) {
	ctx, correlationID := telemetry.ContextWithCorrelationID(ctx, req.GetCorrelationId())
	if correlationID == "" {
		ctx, correlationID = telemetry.EnsureCorrelationID(ctx)
	}

	s.logger.Info("ConvertDocumentToMarkdown started", zap.String("correlation_id", correlationID),
		zap.String("document_id", req.GetDocumentId()),
		zap.String("original_bucket", req.GetOriginalBucket()),
		zap.String("original_object_key", req.GetOriginalObjectKey()),
		zap.String("source_bucket", req.GetSourceBucket()),
		zap.String("source_object_key", req.GetSourceObjectKey()),
		zap.String("source_url", req.GetSourceUrl()),
		zap.String("filename_extension", req.GetFilenameExtension()),
		zap.Boolp("force_ocr", req.ForceOcr))

	if req.GetSourceUrl() != "" && (req.GetSourceBucket() != "" || req.GetSourceObjectKey() != "") {
		return nil, status.Error(codes.InvalidArgument, "provide either source_url or source bucket/object key")
	}

	if (req.GetSourceBucket() == "") != (req.GetSourceObjectKey() == "") {
		return nil, status.Error(codes.InvalidArgument, "source_bucket and source_object_key must be set together")
	}

	result, err := s.usecase.SubmitConversion(ctx, usecase.SubmitConversionRequest{
		DocumentID:        req.GetDocumentId(),
		OriginalBucket:    req.GetOriginalBucket(),
		OriginalObjectKey: req.GetOriginalObjectKey(),
		SourceBucket:      req.GetSourceBucket(),
		SourceObjectKey:   req.GetSourceObjectKey(),
		SourceURL:         req.GetSourceUrl(),
		MarkdownBucket:    req.GetMarkdownBucket(),
		MarkdownObjectKey: req.GetMarkdownObjectKey(),
		FilenameExtension: req.GetFilenameExtension(),
		ForceOCR:          req.ForceOcr,
		CorrelationID:     correlationID,
	})
	if err != nil {
		return nil, s.handleError(err)
	}

	task := result.Task
	if task == nil {
		return nil, status.Error(codes.Internal, "task is not available")
	}

	markdownBucket := result.MarkdownBucket
	markdownKey := result.MarkdownObjectKey

	if req.GetWaitForResult() {
		task, err = s.usecase.WaitForResult(ctx, task.ID)
		if err != nil {
			return nil, s.handleError(err)
		}
		markdownBucket, markdownKey = usecase.ResolveOutput(task, markdownBucket, markdownKey)
	}

	return &transformerv1.ConvertDocumentToMarkdownResponse{
		QueueId:           task.ID.String(),
		Status:            string(task.Status),
		MarkdownBucket:    markdownBucket,
		MarkdownObjectKey: markdownKey,
		CorrelationId:     correlationID,
	}, nil
}

func (s *TransformerAPI) ConvertDocxToMarkdown(ctx context.Context, req *transformerv1.ConvertDocxToMarkdownRequest) (*transformerv1.ConvertDocxToMarkdownResponse, error) {
	ctx, correlationID := telemetry.ContextWithCorrelationID(ctx, req.GetCorrelationId())
	if correlationID == "" {
		ctx, correlationID = telemetry.EnsureCorrelationID(ctx)
	}

	s.logger.Info("ConvertDocxToMarkdown started", zap.String("correlation_id", correlationID),
		zap.String("document_id", req.GetDocumentId()),
		zap.String("filename", req.GetFilename()),
	)

	if (len(req.GetDocxContent()) == 0 && req.GetDocxUrl() == "") || (len(req.GetDocxContent()) > 0 && req.GetDocxUrl() != "") {
		return nil, status.Error(codes.InvalidArgument, "provide either docx_content or docx_url")
	}

	filename := usecase.ResolveFilenameFromURL(req.GetDocxUrl(), req.GetFilename())
	content := req.GetDocxContent()
	if len(content) == 0 {
		data, err := s.usecase.FetchContentByURL(ctx, req.GetDocxUrl())
		if err != nil {
			return nil, s.handleError(err)
		}
		content = data
	}

	result, err := s.usecase.ConvertDocxContent(ctx, filename, content)
	if err != nil {
		return nil, s.handleError(err)
	}

	return &transformerv1.ConvertDocxToMarkdownResponse{
		Markdown:      string(result.Content),
		MediaType:     result.MediaType,
		Charset:       result.Charset,
		CorrelationId: correlationID,
	}, nil
}

func (s *TransformerAPI) handleError(err error) error {
	switch {
	case errors.Is(err, usecase.ErrInvalidArgument):
		return status.Error(codes.InvalidArgument, err.Error())
	case errors.Is(err, usecase.ErrQueueLimitReached):
		return status.Error(codes.ResourceExhausted, "queue limit reached")
	case errors.Is(err, usecase.ErrNotFound):
		return status.Error(codes.NotFound, "queue task not found")
	case errors.Is(err, usecase.ErrResultNotReady), errors.Is(err, transformations.ErrPollingAttemptsExceeded):
		return status.Error(codes.DeadlineExceeded, err.Error())
	case errors.Is(err, context.Canceled):
		return status.Error(codes.Canceled, err.Error())
	case errors.Is(err, context.DeadlineExceeded):
		return status.Error(codes.DeadlineExceeded, err.Error())
	default:
		return status.Error(codes.Internal, err.Error())
	}
}

func Run(ctx context.Context, logger *zap.Logger, uc TransformerUseCase, port int) error {
	address := fmt.Sprintf(":%d", port)
	listener, err := net.Listen("tcp", address)
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}

	handler := otelgrpc.NewServerHandler(otelgrpc.WithTracerProvider(otel.GetTracerProvider()))
	srv := grpc.NewServer(grpc.StatsHandler(handler))

	transformerv1.RegisterTransformerServiceServer(srv, NewTransformerAPI(uc, logger))

	go func() {
		<-ctx.Done()
		srv.GracefulStop()
	}()

	logger.Info("starting gRPC server", zap.String("address", address))

	if err := srv.Serve(listener); err != nil {
		return fmt.Errorf("serve: %w", err)
	}

	return nil
}
