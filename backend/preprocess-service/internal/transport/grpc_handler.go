package transport

import (
	"context"
	"errors"
	"time"

	"go.uber.org/zap"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
	"preprocess-service/internal/domain"
	"preprocess-service/internal/usecase/submit"

	preprocessv1 "preprocess-service/proto/backend/preprocess/v1"
)

type PreprocessHandler struct {
	preprocessv1.UnimplementedPreprocessServiceServer
	service *submit.Service
	logger  *zap.Logger
}

func NewPreprocessHandler(service *submit.Service, logger *zap.Logger) *PreprocessHandler {
	if logger == nil {
		logger = zap.NewNop()
	}

	return &PreprocessHandler{
		service: service,
		logger:  logger,
	}
}

func (h *PreprocessHandler) SubmitPreprocess(ctx context.Context, req *preprocessv1.SubmitPreprocessRequest) (*preprocessv1.SubmitPreprocessResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "request is required")
	}

	correlationID := req.GetCorrelationId()
	if correlationID == "" {
		correlationID = correlationIDFromContext(ctx)
	}

	logger := h.logger.With(
		zap.String("job_id", ""),
		zap.String("document_id", req.GetDocumentId()),
		zap.String("correlation_id", correlationID),
		zap.String("source_bucket", req.GetSourceBucket()),
		zap.String("source_key", req.GetSourceKey()),
		zap.String("status", domain.StatusPending.String()),
	)
	logger.Info("handling SubmitPreprocess request")

	job, err := h.service.SubmitPreprocess(ctx, submit.SubmitCommand{
		DocumentID:    req.GetDocumentId(),
		SourceBucket:  req.GetSourceBucket(),
		SourceKey:     req.GetSourceKey(),
		SourceETag:    req.GetSourceEtag(),
		TargetBucket:  req.GetTargetBucket(),
		TargetKey:     req.GetTargetKey(),
		CallbackURL:   req.GetCallbackUrl(),
		CorrelationID: correlationID,
	})
	if err != nil {
		logger.With(
			zap.String("status", status.Code(err).String()),
		).Error("SubmitPreprocess failed", zap.Error(err))
		return nil, h.handleError(err)
	}

	if correlationID == "" {
		correlationID = job.CorrelationID
	}

	logger.With(
		zap.String("job_id", job.ID),
		zap.String("status", job.Status.String()),
		zap.String("correlation_id", correlationID),
	).Info("SubmitPreprocess succeeded")

	return &preprocessv1.SubmitPreprocessResponse{
		JobId:           job.ID,
		Status:          toProtoStatus(job.Status),
		ErrorMessage:    job.ErrorMessage,
		CorrelationId:   correlationID,
		Attempts:        int32(job.Attempts),
		MaxAttempts:     int32(job.MaxAttempts),
		NextRunAt:       timestamppb.New(job.NextRunAt),
		StartedAt:       timestampOrNil(job.StartedAt),
		FinishedAt:      timestampOrNil(job.FinishedAt),
		TargetMediaType: job.TargetMediaType,
	}, nil
}

func (h *PreprocessHandler) GetPreprocessStatus(ctx context.Context, req *preprocessv1.GetPreprocessStatusRequest) (*preprocessv1.GetPreprocessStatusResponse, error) {
	if req == nil || req.GetJobId() == "" {
		return nil, status.Error(codes.InvalidArgument, "job_id is required")
	}

	logger := h.logger.With(
		zap.String("job_id", req.GetJobId()),
		zap.String("document_id", ""),
		zap.String("correlation_id", req.GetCorrelationId()),
		zap.String("source_bucket", ""),
		zap.String("source_key", ""),
		zap.String("status", domain.StatusUnspecified.String()),
	)
	logger.Info("handling GetPreprocessStatus request")

	job, err := h.service.GetStatus(ctx, submit.StatusQuery{
		JobID:         req.GetJobId(),
		CorrelationID: req.GetCorrelationId(),
	})
	if err != nil {
		logger.With(
			zap.String("status", status.Code(err).String()),
		).Error("GetPreprocessStatus failed", zap.Error(err))
		return nil, h.handleError(err)
	}

	logger.With(
		zap.String("document_id", job.DocumentID),
		zap.String("correlation_id", job.CorrelationID),
		zap.String("source_bucket", job.SourceBucket),
		zap.String("source_key", job.SourceKey),
		zap.String("status", job.Status.String()),
	).Info("GetPreprocessStatus succeeded")

	return &preprocessv1.GetPreprocessStatusResponse{
		JobId:           job.ID,
		DocumentId:      job.DocumentID,
		SourceBucket:    job.SourceBucket,
		SourceKey:       job.SourceKey,
		TargetBucket:    job.TargetBucket,
		TargetKey:       job.TargetKey,
		Status:          toProtoStatus(job.Status),
		ErrorMessage:    job.ErrorMessage,
		ErrorCode:       job.ErrorCode,
		ErrorDetails:    job.ErrorDetails,
		CorrelationId:   job.CorrelationID,
		UpdatedAt:       timestamppb.New(job.UpdatedAt),
		Attempts:        int32(job.Attempts),
		MaxAttempts:     int32(job.MaxAttempts),
		NextRunAt:       timestamppb.New(job.NextRunAt),
		StartedAt:       timestampOrNil(job.StartedAt),
		FinishedAt:      timestampOrNil(job.FinishedAt),
		TargetMediaType: job.TargetMediaType,
	}, nil
}

func (h *PreprocessHandler) handleError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, submit.ErrInvalidRequest):
		return status.Error(codes.InvalidArgument, err.Error())
	case errors.Is(err, submit.ErrJobNotFound):
		return status.Error(codes.NotFound, "job not found")
	default:
		return status.Errorf(codes.Internal, "unexpected error: %v", err)
	}
}

func timestampOrNil(t time.Time) *timestamppb.Timestamp {
	if t.IsZero() {
		return nil
	}

	return timestamppb.New(t)
}
