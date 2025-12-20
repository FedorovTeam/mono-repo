package usecase

import (
	"context"
	"errors"
	"fmt"
	"path"
	"strings"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/mono-repo/table-extractor-service/internal/domain"
	"github.com/mono-repo/table-extractor-service/internal/repository"
	"github.com/mono-repo/table-extractor-service/internal/telemetry"
)

var (
	ErrInvalidArgument   = errors.New("invalid argument")
	ErrQueueLimitReached = errors.New("queue limit reached")
	ErrNotFound          = errors.New("job not found")
)

type ExtractTablesRequest struct {
	DocumentID        string
	OriginalBucket    string
	OriginalObjectKey string
	SourceURL         string
	ArtifactsBucket   string
	ArtifactsPrefix   string
	WaitForResult     bool
	CorrelationID     string
}

type Config struct {
	QueueLimit             int
	DefaultArtifactsBucket string
	DefaultArtifactsPrefix string
}

type TableExtractorUseCase struct {
	repo   repository.TableExtractionRepository
	cfg    Config
	logger *zap.Logger
}

func NewTableExtractorUseCase(repo repository.TableExtractionRepository, cfg Config, logger *zap.Logger) *TableExtractorUseCase {
	if logger == nil {
		logger = zap.NewNop()
	}
	return &TableExtractorUseCase{
		repo:   repo,
		cfg:    cfg,
		logger: logger,
	}
}

func (uc *TableExtractorUseCase) ExtractTablesFromPDF(ctx context.Context, req ExtractTablesRequest) (domain.TableExtractionJob, error) {
	if strings.TrimSpace(req.DocumentID) == "" {
		return domain.TableExtractionJob{}, fmt.Errorf("%w: document_id is required", ErrInvalidArgument)
	}
	if req.OriginalBucket == "" && req.SourceURL == "" {
		return domain.TableExtractionJob{}, fmt.Errorf("%w: provide original_bucket/object_key or source_url", ErrInvalidArgument)
	}
	if req.OriginalBucket != "" && req.OriginalObjectKey == "" {
		return domain.TableExtractionJob{}, fmt.Errorf("%w: original_object_key is required when original_bucket is set", ErrInvalidArgument)
	}
	if req.SourceURL != "" && (req.OriginalBucket != "" || req.OriginalObjectKey != "") {
		return domain.TableExtractionJob{}, fmt.Errorf("%w: source_url and original bucket/object cannot be used together", ErrInvalidArgument)
	}

	ctx, correlationID := telemetry.EnsureCorrelationID(ctx)
	if req.CorrelationID != "" {
		correlationID = req.CorrelationID
		ctx = telemetry.ContextWithCorrelationID(ctx, correlationID)
	}

	now := time.Now().UTC()
	artifactsBucket := req.ArtifactsBucket
	if artifactsBucket == "" {
		artifactsBucket = uc.cfg.DefaultArtifactsBucket
	}
	if artifactsBucket == "" {
		return domain.TableExtractionJob{}, fmt.Errorf("%w: artifacts_bucket is required", ErrInvalidArgument)
	}
	jobID := uuid.New()
	prefix := req.ArtifactsPrefix
	if prefix == "" {
		prefix = path.Join(uc.cfg.DefaultArtifactsPrefix, req.DocumentID, jobID.String())
	}

	job := domain.TableExtractionJob{
		ID:              jobID,
		DocumentID:      req.DocumentID,
		Original:        domain.ArtifactLocation{Bucket: req.OriginalBucket, ObjectKey: req.OriginalObjectKey},
		ArtifactsBucket: artifactsBucket,
		ArtifactsPrefix: prefix,
		Artifacts:       domain.BuildArtifacts(artifactsBucket, prefix),
		Status:          domain.StatusPending,
		Attempts:        0,
		CorrelationID:   correlationID,
		CreatedAt:       now,
		UpdatedAt:       now,
	}

	job, _, err := uc.repo.UpsertOrGetActive(ctx, job, uc.cfg.QueueLimit)
	if err != nil {
		if errors.Is(err, repository.ErrQueueLimitReached) {
			return domain.TableExtractionJob{}, ErrQueueLimitReached
		}
		return domain.TableExtractionJob{}, err
	}

	return job, nil
}

func (uc *TableExtractorUseCase) GetJob(ctx context.Context, id uuid.UUID) (domain.TableExtractionJob, error) {
	job, ok, err := uc.repo.GetByID(ctx, id)
	if err != nil {
		return domain.TableExtractionJob{}, err
	}
	if !ok {
		return domain.TableExtractionJob{}, ErrNotFound
	}
	return job, nil
}
