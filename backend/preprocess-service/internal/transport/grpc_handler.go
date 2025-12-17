package transport

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	"preprocess-service/internal/domain"
	preprocessv1 "preprocess-service/proto/backend/preprocess/v1"
)

type PreprocessHandler struct {
	preprocessv1.UnimplementedPreprocessServiceServer
	repo            domain.JobRepository
	targetBucket    string
	targetKeyPrefix string
}

func NewPreprocessHandler(repo domain.JobRepository, targetBucket, targetKeyPrefix string) *PreprocessHandler {
	return &PreprocessHandler{
		repo:            repo,
		targetBucket:    targetBucket,
		targetKeyPrefix: strings.TrimSuffix(targetKeyPrefix, "/"),
	}
}

func (h *PreprocessHandler) SubmitPreprocess(ctx context.Context, req *preprocessv1.SubmitPreprocessRequest) (*preprocessv1.SubmitPreprocessResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "request is required")
	}

	targetBucket := req.GetTargetBucket()
	if targetBucket == "" {
		targetBucket = h.targetBucket
	}

	targetKey := req.GetTargetKey()
	if targetKey == "" {
		targetKey = h.defaultTargetKey(req)
	}

	if targetBucket == "" || targetKey == "" {
		return nil, status.Error(codes.InvalidArgument, "target_bucket and target_key are required")
	}

	existing, found, err := h.repo.FindBySource(ctx, req.GetSourceBucket(), req.GetSourceKey(), req.GetSourceEtag())
	if err != nil {
		return nil, status.Errorf(codes.Internal, "find job: %v", err)
	}

	if found {
		return &preprocessv1.SubmitPreprocessResponse{
			JobId:         existing.ID,
			Status:        existing.Status,
			ErrorMessage:  existing.ErrorMessage,
			CorrelationId: req.GetCorrelationId(),
		}, nil
	}

	job := domain.PreprocessJob{
		ID:            uuid.NewString(),
		DocumentID:    req.GetDocumentId(),
		SourceBucket:  req.GetSourceBucket(),
		SourceKey:     req.GetSourceKey(),
		SourceETag:    req.GetSourceEtag(),
		TargetBucket:  targetBucket,
		TargetKey:     targetKey,
		CallbackURL:   req.GetCallbackUrl(),
		CorrelationID: req.GetCorrelationId(),
		Status:        preprocessv1.PreprocessStatus_PREPROCESS_STATUS_PENDING,
		ErrorDetails:  map[string]string{},
		UpdatedAt:     time.Now().UTC(),
	}

	if err := job.Validate(); err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}

	if err := h.repo.Save(ctx, job); err != nil {
		return nil, status.Errorf(codes.Internal, "save job: %v", err)
	}

	return &preprocessv1.SubmitPreprocessResponse{
		JobId:         job.ID,
		Status:        job.Status,
		CorrelationId: job.CorrelationID,
	}, nil
}

func (h *PreprocessHandler) defaultTargetKey(req *preprocessv1.SubmitPreprocessRequest) string {
	baseName := req.GetDocumentId()
	if baseName == "" {
		baseName = uuid.NewString()
	}

	if h.targetKeyPrefix != "" {
		return fmt.Sprintf("%s/%s.docx", h.targetKeyPrefix, baseName)
	}

	return fmt.Sprintf("%s.docx", baseName)
}

func (h *PreprocessHandler) GetPreprocessStatus(ctx context.Context, req *preprocessv1.GetPreprocessStatusRequest) (*preprocessv1.GetPreprocessStatusResponse, error) {
	if req == nil || req.GetJobId() == "" {
		return nil, status.Error(codes.InvalidArgument, "job_id is required")
	}

	job, found, err := h.repo.Get(ctx, req.GetJobId())
	if err != nil {
		return nil, status.Errorf(codes.Internal, "get job: %v", err)
	}
	if !found {
		return nil, status.Error(codes.NotFound, "job not found")
	}

	correlationID := req.GetCorrelationId()
	if correlationID == "" {
		correlationID = job.CorrelationID
	}

	errorDetails := job.ErrorDetails
	if errorDetails == nil {
		errorDetails = map[string]string{}
	}

	return &preprocessv1.GetPreprocessStatusResponse{
		JobId:         job.ID,
		DocumentId:    job.DocumentID,
		SourceBucket:  job.SourceBucket,
		SourceKey:     job.SourceKey,
		TargetBucket:  job.TargetBucket,
		TargetKey:     job.TargetKey,
		Status:        job.Status,
		ErrorMessage:  job.ErrorMessage,
		ErrorCode:     job.ErrorCode,
		ErrorDetails:  errorDetails,
		CorrelationId: correlationID,
		UpdatedAt:     timestamppb.New(job.UpdatedAt),
	}, nil
}
