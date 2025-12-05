package grpcserver

import (
	"context"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"backend-storage-service/internal/storage"
	storagev1 "backend-storage-service/proto/backend/storage/v1"
)

const (
	defaultOrgSlug     = "knowledge"
	defaultProjectSlug = "documents"
)

// Server реализует gRPC-контракт StorageService.
type Server struct {
	storagev1.UnimplementedStorageServiceServer

	client       storage.Client
	keyGenerator *storage.ObjectKeyGenerator
	uploadBucket string
	orgSlug      string
	projectSlug  string
}

// New создаёт сервер с зависимостями для генерации presigned URL.
func New(client storage.Client, keyGenerator *storage.ObjectKeyGenerator, uploadBucket string) *Server {
	orgSlug := defaultOrgSlug
	projectSlug := defaultProjectSlug

	return &Server{
		client:       client,
		keyGenerator: keyGenerator,
		uploadBucket: uploadBucket,
		orgSlug:      orgSlug,
		projectSlug:  projectSlug,
	}
}

// GenerateUploadURL подготавливает presigned PUT ссылку для загрузки документа в MinIO.
func (s *Server) GenerateUploadURL(ctx context.Context, req *storagev1.GenerateUploadURLRequest) (*storagev1.GenerateUploadURLResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "request is required")
	}

	if req.GetDocumentId() == "" || req.GetFileName() == "" {
		return nil, status.Error(codes.InvalidArgument, "document_id и file_name должны быть заполнены")
	}

	if req.GetExpiresInSeconds() <= 0 {
		return nil, status.Error(codes.InvalidArgument, "expires_in_seconds должен быть больше нуля")
	}

	ext := strings.TrimPrefix(filepath.Ext(req.GetFileName()), ".")
	if ext == "" {
		return nil, status.Error(codes.InvalidArgument, "некорректное расширение файла")
	}

	if err := s.ensureBucket(ctx, s.uploadBucket); err != nil {
		return nil, err
	}

	key, err := s.keyGenerator.BuildKey(s.orgSlug, s.projectSlug, req.GetDocumentId(), ext)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "cannot build object key: %v", err)
	}

	headers := http.Header{}
	if correlationID := s.correlationID(ctx, req.GetCorrelationId()); correlationID != "" {
		headers.Set("x-amz-meta-correlation-id", correlationID)
	}

	expires := time.Duration(req.GetExpiresInSeconds()) * time.Second
	url, err := s.client.PresignHeader(ctx, http.MethodPut, s.uploadBucket, key, expires, nil, headers)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "cannot presign upload url: %v", err)
	}

	return &storagev1.GenerateUploadURLResponse{
		UploadUrl:        url.String(),
		Bucket:           s.uploadBucket,
		ObjectKey:        key,
		ExpiresInSeconds: req.GetExpiresInSeconds(),
	}, nil
}

func (s *Server) ensureBucket(ctx context.Context, bucket string) error {
	exists, err := s.client.BucketExists(ctx, bucket)
	if err != nil {
		return status.Errorf(codes.Internal, "cannot check bucket %s: %v", bucket, err)
	}
	if !exists {
		return status.Errorf(codes.FailedPrecondition, "bucket %s недоступен", bucket)
	}
	return nil
}

func (s *Server) correlationID(ctx context.Context, requestValue string) string {
	if requestValue != "" {
		return requestValue
	}

	if md, ok := metadata.FromIncomingContext(ctx); ok {
		if values := md.Get("x-correlation-id"); len(values) > 0 {
			return values[0]
		}
	}

	return ""
}

var _ storagev1.StorageServiceServer = (*Server)(nil)
