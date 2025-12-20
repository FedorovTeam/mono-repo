package server

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/mono-repo/table-extractor-service/internal/domain"
	"github.com/mono-repo/table-extractor-service/internal/telemetry"
	"github.com/mono-repo/table-extractor-service/internal/usecase"
	pb "github.com/mono-repo/table-extractor-service/proto/backend/tableextractor/v1"
)

type ExtractionUseCase interface {
	ExtractTablesFromPDF(ctx context.Context, req usecase.ExtractTablesRequest) (domain.TableExtractionJob, error)
	GetJob(ctx context.Context, id uuid.UUID) (domain.TableExtractionJob, error)
}

type Handler struct {
	pb.UnimplementedTableExtractorServiceServer
	uc     ExtractionUseCase
	logger *zap.Logger
}

func NewHandler(uc ExtractionUseCase, logger *zap.Logger) *Handler {
	if logger == nil {
		logger = zap.NewNop()
	}
	return &Handler{uc: uc, logger: logger}
}

func (h *Handler) ExtractTablesFromPdf(ctx context.Context, req *pb.ExtractTablesFromPdfRequest) (*pb.ExtractTablesFromPdfResponse, error) {
	correlationID := telemetry.CorrelationIDFromContext(ctx)
	if correlationID == "" {
		ctx, correlationID = telemetry.EnsureCorrelationID(ctx)
	}
	ctx = telemetry.WithOutgoingCorrelationID(ctx, correlationID)
	if req.GetCorrelationId() != "" {
		correlationID = req.GetCorrelationId()
		ctx = telemetry.ContextWithCorrelationID(ctx, correlationID)
		ctx = telemetry.WithOutgoingCorrelationID(ctx, correlationID)
	}

	job, err := h.uc.ExtractTablesFromPDF(ctx, usecase.ExtractTablesRequest{
		DocumentID:        req.GetDocumentId(),
		OriginalBucket:    req.GetOriginalBucket(),
		OriginalObjectKey: req.GetOriginalObjectKey(),
		SourceURL:         req.GetSourceUrl(),
		ArtifactsBucket:   req.GetArtifactsBucket(),
		ArtifactsPrefix:   req.GetArtifactsPrefix(),
		WaitForResult:     req.GetWaitForResult(),
		CorrelationID:     correlationID,
	})
	if err != nil {
		return nil, h.mapError(err)
	}

	telemetry.SetCorrelationHeader(ctx, job.CorrelationID)

	return &pb.ExtractTablesFromPdfResponse{
		Job:           toProtoJob(job),
		CorrelationId: job.CorrelationID,
	}, nil
}

func (h *Handler) GetTableExtractionJob(ctx context.Context, req *pb.GetTableExtractionJobRequest) (*pb.GetTableExtractionJobResponse, error) {
	correlationID := telemetry.CorrelationIDFromContext(ctx)
	if correlationID == "" {
		ctx, correlationID = telemetry.EnsureCorrelationID(ctx)
	}
	ctx = telemetry.WithOutgoingCorrelationID(ctx, correlationID)
	if req.GetCorrelationId() != "" {
		correlationID = req.GetCorrelationId()
		ctx = telemetry.ContextWithCorrelationID(ctx, correlationID)
		ctx = telemetry.WithOutgoingCorrelationID(ctx, correlationID)
	}

	jobID, err := uuid.Parse(req.GetQueueId())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid queue_id")
	}

	job, err := h.uc.GetJob(ctx, jobID)
	if err != nil {
		return nil, h.mapError(err)
	}

	telemetry.SetCorrelationHeader(ctx, job.CorrelationID)

	return &pb.GetTableExtractionJobResponse{
		Job:           toProtoJob(job),
		CorrelationId: job.CorrelationID,
	}, nil
}

func (h *Handler) mapError(err error) error {
	switch {
	case errors.Is(err, usecase.ErrInvalidArgument):
		return status.Error(codes.InvalidArgument, err.Error())
	case errors.Is(err, usecase.ErrQueueLimitReached):
		return status.Error(codes.ResourceExhausted, err.Error())
	case errors.Is(err, usecase.ErrNotFound):
		return status.Error(codes.NotFound, err.Error())
	default:
		return status.Error(codes.Internal, err.Error())
	}
}

func toProtoJob(job domain.TableExtractionJob) *pb.TableExtractionJob {
	return &pb.TableExtractionJob{
		QueueId:       job.ID.String(),
		DocumentId:    job.DocumentID,
		Status:        toProtoStatus(job.Status),
		StatusReason:  job.StatusReason,
		Artifacts:     toProtoArtifacts(job.Artifacts),
		CreatedAt:     toProtoTimestamp(job.CreatedAt),
		UpdatedAt:     toProtoTimestamp(job.UpdatedAt),
		CorrelationId: job.CorrelationID,
	}
}

func toProtoArtifacts(artifacts domain.TableArtifacts) *pb.TableArtifacts {
	return &pb.TableArtifacts{
		RawJson:        toProtoArtifact(artifacts.RawJSON),
		NormalizedJson: toProtoArtifact(artifacts.NormalizedJSON),
		Markdown:       toProtoArtifact(artifacts.Markdown),
		Manifest:       toProtoArtifact(artifacts.Manifest),
		DebugPrefix:    toProtoArtifact(artifacts.DebugPrefix),
	}
}

func toProtoArtifact(artifact domain.ArtifactLocation) *pb.ArtifactLocation {
	if artifact.Bucket == "" && artifact.ObjectKey == "" {
		return nil
	}
	return &pb.ArtifactLocation{
		Bucket:    artifact.Bucket,
		ObjectKey: artifact.ObjectKey,
	}
}

func toProtoStatus(status domain.TableExtractionStatus) pb.TableExtractionStatus {
	switch status {
	case domain.StatusPending:
		return pb.TableExtractionStatus_TABLE_EXTRACTION_STATUS_PENDING
	case domain.StatusProcessing:
		return pb.TableExtractionStatus_TABLE_EXTRACTION_STATUS_PROCESSING
	case domain.StatusDone:
		return pb.TableExtractionStatus_TABLE_EXTRACTION_STATUS_DONE
	case domain.StatusFailed:
		return pb.TableExtractionStatus_TABLE_EXTRACTION_STATUS_FAILED
	case domain.StatusFailedRetryable:
		return pb.TableExtractionStatus_TABLE_EXTRACTION_STATUS_FAILED_RETRYABLE
	default:
		return pb.TableExtractionStatus_TABLE_EXTRACTION_STATUS_UNSPECIFIED
	}
}

func toProtoTimestamp(value time.Time) *timestamppb.Timestamp {
	if value.IsZero() {
		return nil
	}
	return timestamppb.New(value)
}
