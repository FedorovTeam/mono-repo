package grpcserver

import (
	"context"
	"errors"
	"strings"

	"github.com/minio/minio-go/v7"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

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
	service      *storage.Service
	keyGenerator *storage.ObjectKeyGenerator
	uploadBucket string
	orgSlug      string
	projectSlug  string
}

// New создаёт сервер с зависимостями для генерации presigned URL.
func New(client storage.Client, keyGenerator *storage.ObjectKeyGenerator, uploadBucket string, service *storage.Service) *Server {
	orgSlug := defaultOrgSlug
	projectSlug := defaultProjectSlug

	return &Server{
		client:       client,
		service:      service,
		keyGenerator: keyGenerator,
		uploadBucket: uploadBucket,
		orgSlug:      orgSlug,
		projectSlug:  projectSlug,
	}
}

// UploadOriginal сохраняет исходный файл документа в бакет MinIO.
func (s *Server) UploadOriginal(ctx context.Context, req *storagev1.UploadOriginalRequest) (*storagev1.UploadObjectResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "request is required")
	}

	orgSlug := s.resolveOrgSlug(ctx, req.GetOrgSlug())
	projectSlug := s.resolveProjectSlug(ctx, req.GetProjectSlug())

	if req.GetDocumentId() == "" {
		return nil, status.Error(codes.InvalidArgument, "document_id должен быть заполнен")
	}

	ext := strings.TrimPrefix(req.GetFilenameExtension(), ".")
	if ext == "" {
		return nil, status.Error(codes.InvalidArgument, "filename_extension должен быть указан")
	}

	if len(req.GetContent()) == 0 {
		return nil, status.Error(codes.InvalidArgument, "content не должен быть пустым")
	}

	s.attachResponseMetadata(ctx, s.correlationID(ctx, ""), orgSlug, projectSlug)

	location, err := s.service.UploadOriginal(ctx, orgSlug, projectSlug, req.GetDocumentId(), ext, req.GetContent())
	if err != nil {
		return nil, s.mapStorageError(err)
	}

	return &storagev1.UploadObjectResponse{
		Bucket:    location.Bucket,
		ObjectKey: location.Key,
		CreatedAt: timestamppb.Now(),
	}, nil
}

// UploadMarkdown сохраняет Markdown-версию документа в бакет MinIO.
func (s *Server) UploadMarkdown(ctx context.Context, req *storagev1.UploadMarkdownRequest) (*storagev1.UploadObjectResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "request is required")
	}

	orgSlug := s.resolveOrgSlug(ctx, req.GetOrgSlug())
	projectSlug := s.resolveProjectSlug(ctx, req.GetProjectSlug())

	if req.GetDocumentId() == "" {
		return nil, status.Error(codes.InvalidArgument, "document_id должен быть заполнен")
	}

	if len(req.GetContent()) == 0 {
		return nil, status.Error(codes.InvalidArgument, "content не должен быть пустым")
	}

	s.attachResponseMetadata(ctx, s.correlationID(ctx, ""), orgSlug, projectSlug)

	location, err := s.service.UploadMarkdown(ctx, orgSlug, projectSlug, req.GetDocumentId(), req.GetContent())
	if err != nil {
		return nil, s.mapStorageError(err)
	}

	return &storagev1.UploadObjectResponse{
		Bucket:    location.Bucket,
		ObjectKey: location.Key,
		CreatedAt: timestamppb.Now(),
	}, nil
}

// GetObject отдает содержимое ранее загруженного объекта.
func (s *Server) GetObject(ctx context.Context, req *storagev1.GetObjectRequest) (*storagev1.GetObjectResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "request is required")
	}

	if req.GetBucket() == "" || req.GetObjectKey() == "" {
		return nil, status.Error(codes.InvalidArgument, "bucket и object_key должны быть заполнены")
	}

	correlationID := s.correlationID(ctx, "")
	s.attachResponseMetadata(ctx, correlationID, s.resolveOrgSlug(ctx, ""), s.resolveProjectSlug(ctx, ""))

	content, err := s.service.DownloadObject(ctx, req.GetBucket(), req.GetObjectKey())
	if err != nil {
		return nil, s.mapStorageError(err)
	}

	return &storagev1.GetObjectResponse{
		Bucket:    req.GetBucket(),
		ObjectKey: req.GetObjectKey(),
		Content:   content,
		UpdatedAt: timestamppb.Now(),
	}, nil
}

// GenerateUploadURL подготавливает presigned PUT ссылку для загрузки документа в MinIO.
func (s *Server) GenerateUploadURL(ctx context.Context, req *storagev1.GenerateUploadURLRequest) (*storagev1.GenerateUploadURLResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "request is required")
	}

	if req.GetDocumentId() == "" || req.GetFileName() == "" {
		return nil, status.Error(codes.InvalidArgument, "document_id и file_name должны быть заполнены")
	}

	correlationID := s.correlationID(ctx, req.GetCorrelationId())
	upload, err := s.service.GenerateUploadURL(ctx, s.uploadBucket, storage.GenerateUploadURLParams{
		OrgSlug:          s.orgSlug,
		ProjectSlug:      s.projectSlug,
		DocumentID:       req.GetDocumentId(),
		FileName:         req.GetFileName(),
		ContentLength:    req.GetContentLength(),
		CorrelationID:    correlationID,
		ExpiresInSeconds: req.GetExpiresInSeconds(),
	})
	if err != nil {
		return nil, s.mapStorageError(err)
	}

	s.attachResponseMetadata(ctx, correlationID, s.orgSlug, s.projectSlug)

	return &storagev1.GenerateUploadURLResponse{
		UploadUrl:        upload.UploadURL.String(),
		Bucket:           upload.Bucket,
		ObjectKey:        upload.ObjectKey,
		ExpiresInSeconds: upload.ExpiresInSeconds,
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

func (s *Server) resolveOrgSlug(ctx context.Context, requestValue string) string {
	if requestValue != "" {
		return requestValue
	}

	if md, ok := metadata.FromIncomingContext(ctx); ok {
		if values := md.Get("org-slug"); len(values) > 0 && values[0] != "" {
			return values[0]
		}
	}

	return s.orgSlug
}

func (s *Server) resolveProjectSlug(ctx context.Context, requestValue string) string {
	if requestValue != "" {
		return requestValue
	}

	if md, ok := metadata.FromIncomingContext(ctx); ok {
		if values := md.Get("project-slug"); len(values) > 0 && values[0] != "" {
			return values[0]
		}
	}

	return s.projectSlug
}

func (s *Server) attachResponseMetadata(ctx context.Context, correlationID, orgSlug, projectSlug string) {
	pairs := []string{
		"x-correlation-id", correlationID,
		"org-slug", orgSlug,
		"project-slug", projectSlug,
	}

	if correlationID != "" {
		pairs = append(pairs, "x-amz-meta-correlation-id", correlationID)
	}

	md := metadata.Pairs(pairs...)
	_ = grpc.SetHeader(ctx, md)
}

func (s *Server) mapStorageError(err error) error {
	if err == nil {
		return nil
	}

	if errors.Is(err, storage.ErrInvalidArgument) {
		return status.Error(codes.InvalidArgument, err.Error())
	}

	var minioErr minio.ErrorResponse
	if errors.As(err, &minioErr) {
		switch minioErr.Code {
		case "NoSuchBucket":
			return status.Errorf(codes.FailedPrecondition, "bucket недоступен: %v", err)
		case "NoSuchKey":
			return status.Errorf(codes.NotFound, "object not found: %v", err)
		}
	}

	if strings.Contains(err.Error(), "does not exist") {
		return status.Errorf(codes.FailedPrecondition, "bucket недоступен: %v", err)
	}

	return status.Errorf(codes.Internal, "storage operation failed: %v", err)
}

var _ storagev1.StorageServiceServer = (*Server)(nil)
