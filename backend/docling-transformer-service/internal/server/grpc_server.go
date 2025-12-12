package server

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
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
	queueRepo       repository.QueueRepository
	converter       converter.Converter
	markdownBucket  string
	queueLimit      int
	pollInterval    time.Duration
	pollMaxInterval time.Duration
	pollAttempts    int
	logger          *zap.Logger
	httpClient      *http.Client
}

// NewTransformerAPI конструирует обработчик с переданным конвертером.
func NewTransformerAPI(queueRepo repository.QueueRepository, markdownBucket string, queueLimit int, conv converter.Converter, pollInterval, pollMaxInterval time.Duration, pollAttempts int, logger *zap.Logger) *TransformerAPI {
	if logger == nil {
		logger = zap.NewNop()
	}
	if pollInterval <= 0 {
		pollInterval = 2 * time.Second
	}
	if pollMaxInterval <= 0 {
		pollMaxInterval = 10 * time.Second
	}
	if pollAttempts <= 0 {
		pollAttempts = 10
	}

	return &TransformerAPI{
		queueRepo:       queueRepo,
		converter:       conv,
		markdownBucket:  markdownBucket,
		queueLimit:      queueLimit,
		pollInterval:    pollInterval,
		pollMaxInterval: pollMaxInterval,
		pollAttempts:    pollAttempts,
		logger:          logger,
		httpClient:      &http.Client{Timeout: 30 * time.Second},
	}
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

	existingTask, err := s.queueRepo.GetLatestByDocument(ctx, req.GetDocumentId())
	if err != nil {
		s.logger.Error("fetch latest task failed", zap.String("correlation_id", correlationID), zap.Error(err))
		return nil, status.Errorf(codes.Internal, "fetch latest task: %v", err)
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

	if existingTask != nil && existingTask.Status != repository.StatusFailed {
		bucket := markdownBucket
		if existingTask.MarkdownBucket != "" {
			bucket = existingTask.MarkdownBucket
		}

		key := markdownKey
		if existingTask.MarkdownObjectKey != "" {
			key = existingTask.MarkdownObjectKey
		}

		s.logger.Info("ConvertDocumentToMarkdown reused existing task", zap.String("correlation_id", correlationID),
			zap.String("queue_id", existingTask.ID.String()), zap.String("status", string(existingTask.Status)))

		if req.GetWaitForResult() {
			task, waitErr := s.waitForTask(ctx, existingTask.ID)
			if waitErr != nil {
				return nil, waitErr
			}

			bucket, key = resolveOutput(task, bucket, key)

			return &transformerv1.ConvertDocumentToMarkdownResponse{
				QueueId:           task.ID.String(),
				Status:            string(task.Status),
				MarkdownBucket:    bucket,
				MarkdownObjectKey: key,
			}, nil
		}

		return &transformerv1.ConvertDocumentToMarkdownResponse{
			QueueId:           existingTask.ID.String(),
			Status:            string(existingTask.Status),
			MarkdownBucket:    bucket,
			MarkdownObjectKey: key,
		}, nil
	}

	if s.queueLimit > 0 {
		pending, processing, waiting, err := s.queueRepo.CountByStatus(ctx)
		if err != nil {
			s.logger.Error("count queue tasks failed", zap.String("correlation_id", correlationID), zap.Error(err))
			return nil, status.Errorf(codes.Internal, "count queue tasks: %v", err)
		}

		currentSize := pending + processing + waiting
		if currentSize >= s.queueLimit {
			s.logger.Warn("queue limit reached", zap.String("correlation_id", correlationID),
				zap.Int("queue_limit", s.queueLimit),
				zap.Int("pending", pending), zap.Int("processing", processing), zap.Int("waiting_docling", waiting))
			return nil, status.Error(codes.ResourceExhausted, "queue limit reached")
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

	if req.GetWaitForResult() {
		task, waitErr := s.waitForTask(ctx, queueID)
		if waitErr != nil {
			return nil, waitErr
		}

		bucket, key := resolveOutput(task, markdownBucket, markdownKey)

		return &transformerv1.ConvertDocumentToMarkdownResponse{
			QueueId:           task.ID.String(),
			Status:            string(task.Status),
			MarkdownBucket:    bucket,
			MarkdownObjectKey: key,
		}, nil
	}

	return &transformerv1.ConvertDocumentToMarkdownResponse{
		QueueId:           queueID.String(),
		Status:            string(repository.StatusPending),
		MarkdownBucket:    markdownBucket,
		MarkdownObjectKey: markdownKey,
	}, nil
}

// ConvertDocxToMarkdown принимает содержимое DOCX либо ссылку, отправляет задачу в Docling и дожидается результата.
func (s *TransformerAPI) ConvertDocxToMarkdown(ctx context.Context, req *transformerv1.ConvertDocxToMarkdownRequest) (*transformerv1.ConvertDocxToMarkdownResponse, error) {
	ctx, correlationID := telemetry.EnsureCorrelationID(ctx)

	if s.converter == nil {
		return nil, status.Error(codes.Unimplemented, "converter is not configured")
	}

	if (len(req.GetDocxContent()) == 0 && req.GetDocxUrl() == "") || (len(req.GetDocxContent()) > 0 && req.GetDocxUrl() != "") {
		return nil, status.Error(codes.InvalidArgument, "provide either docx_content or docx_url")
	}

	filename := strings.TrimSpace(req.GetFilename())
	if filename == "" && req.GetDocxUrl() != "" {
		if parsed, err := url.Parse(req.GetDocxUrl()); err == nil {
			filename = path.Base(parsed.Path)
		}
	}
	if filename == "" {
		filename = "document.docx"
	}

	content := req.GetDocxContent()
	if len(content) == 0 {
		data, err := s.fetchByURL(ctx, req.GetDocxUrl())
		if err != nil {
			return nil, err
		}
		content = data
	}

	contentType, err := converter.ResolveContentTypeByExtension(filepath.Ext(filename))
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "filename must point to supported doc: %v", err)
	}

	jobID, err := s.converter.Submit(ctx, converter.DoclingConvertRequest{FileName: filename, Content: content, ContentType: contentType})
	if err != nil {
		s.logger.Error("docx submit failed", zap.String("correlation_id", correlationID), zap.Error(err))
		return nil, status.Errorf(codes.Internal, "submit docx: %v", err)
	}

	result, err := s.pollUntilReady(ctx, jobID)
	if err != nil {
		s.logger.Error("docx conversion failed", zap.String("correlation_id", correlationID), zap.String("job_id", jobID), zap.Error(err))
		return nil, status.Errorf(codes.DeadlineExceeded, "docling result: %v", err)
	}

	return &transformerv1.ConvertDocxToMarkdownResponse{
		Markdown:  string(result.Content),
		MediaType: result.MediaType,
		Charset:   result.Charset,
	}, nil
}

func (s *TransformerAPI) waitForTask(ctx context.Context, queueID uuid.UUID) (*repository.TransformationTask, error) {
	backoff := s.pollInterval
	if backoff <= 0 {
		backoff = 2 * time.Second
	}
	maxInterval := s.pollMaxInterval
	if maxInterval <= 0 {
		maxInterval = 10 * time.Second
	}

	attempts := s.pollAttempts
	if attempts <= 0 {
		attempts = 10
	}

	for attempt := 1; attempt <= attempts; attempt++ {
		task, err := s.queueRepo.GetByID(ctx, queueID)
		if err != nil {
			s.logger.Error("wait task fetch failed", zap.Error(err), zap.String("queue_id", queueID.String()))
			return nil, status.Errorf(codes.Internal, "fetch task status: %v", err)
		}
		if task == nil {
			return nil, status.Error(codes.NotFound, "queue task not found")
		}

		if task.Status == repository.StatusDone || task.Status == repository.StatusFailed {
			return task, nil
		}

		waitFor := backoff
		if task.NextPollAt != nil {
			until := time.Until(*task.NextPollAt)
			if until > 0 {
				waitFor = until
			}
		}

		if attempt == attempts {
			break
		}

		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(waitFor):
		}

		backoff = backoff * 2
		if backoff > maxInterval {
			backoff = maxInterval
		}
	}

	return nil, status.Error(codes.DeadlineExceeded, "conversion result not ready")
}

func resolveOutput(task *repository.TransformationTask, fallbackBucket, fallbackKey string) (string, string) {
	bucket := fallbackBucket
	key := fallbackKey

	if task != nil {
		if task.MarkdownBucket != "" {
			bucket = task.MarkdownBucket
		}
		if task.MarkdownObjectKey != "" {
			key = task.MarkdownObjectKey
		}
	}

	return bucket, key
}

func (s *TransformerAPI) pollUntilReady(ctx context.Context, jobID string) (converter.DoclingConvertResult, error) {
	backoff := s.pollInterval

	for attempt := 1; attempt <= s.pollAttempts; attempt++ {
		statusResult, err := s.converter.CheckStatus(ctx, jobID)
		if err != nil {
			return converter.DoclingConvertResult{}, err
		}
		if statusResult.Result != nil {
			return *statusResult.Result, nil
		}

		if attempt == s.pollAttempts {
			break
		}

		select {
		case <-ctx.Done():
			return converter.DoclingConvertResult{}, ctx.Err()
		case <-time.After(backoff):
		}

		backoff = backoff * 2
		if s.pollMaxInterval > 0 && backoff > s.pollMaxInterval {
			backoff = s.pollMaxInterval
		}
	}

	return converter.DoclingConvertResult{}, errors.New("docling result not ready")
}

func (s *TransformerAPI) fetchByURL(ctx context.Context, rawURL string) ([]byte, error) {
	if rawURL == "" {
		return nil, status.Error(codes.InvalidArgument, "docx_url is required when content is empty")
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "build request: %v", err)
	}

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, status.Errorf(codes.Unavailable, "fetch docx: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return nil, status.Errorf(codes.InvalidArgument, "docx_url responded with %d: %s", resp.StatusCode, string(body))
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "read docx: %v", err)
	}

	if len(data) == 0 {
		return nil, status.Error(codes.InvalidArgument, "docx_url returned empty body")
	}

	return data, nil
}

// Run запускает gRPC сервер на указанном порте.
func Run(ctx context.Context, logger *zap.Logger, queueRepo repository.QueueRepository, markdownBucket string, queueLimit int, conv converter.Converter, doclingCfg config.DoclingConfig, port int, obsCfg config.ObservabilityConfig) error {
	srv := newGRPCServer(logger, obsCfg)
	transformerv1.RegisterTransformerServiceServer(srv, NewTransformerAPI(queueRepo, markdownBucket, queueLimit, conv, doclingCfg.StatusInitialInterval, doclingCfg.StatusMaxInterval, doclingCfg.StatusMaxAttempts, logger))

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
