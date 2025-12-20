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

	if existing, ok, err := uc.repo.GetByDocumentID(ctx, req.DocumentID); err != nil {
		return domain.TableExtractionJob{}, err
	} else if ok && existing.Status != domain.StatusFailed {
		return existing, nil
	}

	if uc.cfg.QueueLimit > 0 {
		count, err := uc.repo.CountActive(ctx)
		if err != nil {
			return domain.TableExtractionJob{}, err
		}
		if count >= uc.cfg.QueueLimit {
			return domain.TableExtractionJob{}, ErrQueueLimitReached
		}
	}

	ctx, correlationID := telemetry.EnsureCorrelationID(ctx)
	if req.CorrelationID != "" {
		correlationID = req.CorrelationID
		ctx = telemetry.ContextWithCorrelationID(ctx, correlationID)
	}

	jobID := uuid.New()
	now := time.Now().UTC()
	artifactsBucket := req.ArtifactsBucket
	if artifactsBucket == "" {
		artifactsBucket = uc.cfg.DefaultArtifactsBucket
	}
	if artifactsBucket == "" {
		return domain.TableExtractionJob{}, fmt.Errorf("%w: artifacts_bucket is required", ErrInvalidArgument)
	}
	prefix := req.ArtifactsPrefix
	if prefix == "" {
		prefix = path.Join(uc.cfg.DefaultArtifactsPrefix, req.DocumentID, jobID.String())
	}
	artifacts := uc.buildArtifacts(prefix, artifactsBucket)

	job := domain.TableExtractionJob{
		ID:            jobID,
		DocumentID:    req.DocumentID,
		Status:        domain.StatusPending,
		Artifacts:     artifacts,
		CorrelationID: correlationID,
		CreatedAt:     now,
		UpdatedAt:     now,
	}

	job, err := uc.repo.Create(ctx, job)
	if err != nil {
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

func (uc *TableExtractorUseCase) buildArtifacts(prefix, bucket string) domain.TableArtifacts {
	return domain.TableArtifacts{
		RawJSON: domain.ArtifactLocation{
			Bucket:    bucket,
			ObjectKey: path.Join(prefix, "tables.raw.json"),
		},
		NormalizedJSON: domain.ArtifactLocation{
			Bucket:    bucket,
			ObjectKey: path.Join(prefix, "tables.normalized.json"),
		},
		Markdown: domain.ArtifactLocation{
			Bucket:    bucket,
			ObjectKey: path.Join(prefix, "tables.md"),
		},
		Manifest: domain.ArtifactLocation{
			Bucket:    bucket,
			ObjectKey: path.Join(prefix, "manifest.json"),
		},
		DebugPrefix: domain.ArtifactLocation{
			Bucket:    bucket,
			ObjectKey: path.Join(prefix, "debug"),
		},
	}
}
