package transport

import (
	"context"
	"errors"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
	"preprocess-service/internal/usecase/submit"

	preprocessv1 "preprocess-service/proto/backend/preprocess/v1"
)

type PreprocessHandler struct {
	preprocessv1.UnimplementedPreprocessServiceServer
	service *submit.Service
}

func NewPreprocessHandler(service *submit.Service) *PreprocessHandler {
	return &PreprocessHandler{
		service: service,
	}
}

func (h *PreprocessHandler) SubmitPreprocess(ctx context.Context, req *preprocessv1.SubmitPreprocessRequest) (*preprocessv1.SubmitPreprocessResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "request is required")
	}

	job, err := h.service.SubmitPreprocess(ctx, submit.SubmitCommand{
		DocumentID:    req.GetDocumentId(),
		SourceBucket:  req.GetSourceBucket(),
		SourceKey:     req.GetSourceKey(),
		SourceETag:    req.GetSourceEtag(),
		TargetBucket:  req.GetTargetBucket(),
		TargetKey:     req.GetTargetKey(),
		CallbackURL:   req.GetCallbackUrl(),
		CorrelationID: req.GetCorrelationId(),
	})
	if err != nil {
		return nil, h.handleError(err)
	}

	correlationID := req.GetCorrelationId()
	if correlationID == "" {
		correlationID = job.CorrelationID
	}

	return &preprocessv1.SubmitPreprocessResponse{
		JobId:         job.ID,
		Status:        job.Status.ToProto(),
		ErrorMessage:  job.ErrorMessage,
		CorrelationId: correlationID,
	}, nil
}

func (h *PreprocessHandler) GetPreprocessStatus(ctx context.Context, req *preprocessv1.GetPreprocessStatusRequest) (*preprocessv1.GetPreprocessStatusResponse, error) {
	if req == nil || req.GetJobId() == "" {
		return nil, status.Error(codes.InvalidArgument, "job_id is required")
	}

	job, err := h.service.GetStatus(ctx, submit.StatusQuery{
		JobID:         req.GetJobId(),
		CorrelationID: req.GetCorrelationId(),
	})
	if err != nil {
		return nil, h.handleError(err)
	}

	return &preprocessv1.GetPreprocessStatusResponse{
		JobId:         job.ID,
		DocumentId:    job.DocumentID,
		SourceBucket:  job.SourceBucket,
		SourceKey:     job.SourceKey,
		TargetBucket:  job.TargetBucket,
		TargetKey:     job.TargetKey,
		Status:        job.Status.ToProto(),
		ErrorMessage:  job.ErrorMessage,
		ErrorCode:     job.ErrorCode,
		ErrorDetails:  job.ErrorDetails,
		CorrelationId: job.CorrelationID,
		UpdatedAt:     timestamppb.New(job.UpdatedAt),
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
