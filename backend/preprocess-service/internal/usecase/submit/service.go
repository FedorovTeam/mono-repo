package submit

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"
	"google.golang.org/grpc/metadata"

	"preprocess-service/internal/domain"
)

var (
	ErrInvalidRequest = errors.New("invalid submit request")
	ErrJobNotFound    = errors.New("job not found")
)

type SubmitCommand struct {
	DocumentID    string
	SourceBucket  string
	SourceKey     string
	SourceETag    string
	TargetBucket  string
	TargetKey     string
	CallbackURL   string
	CorrelationID string
}

type StatusQuery struct {
	JobID         string
	CorrelationID string
}

type Service struct {
	repo            domain.PreprocessJobRepository
	targetBucket    string
	targetKeyPrefix string
	logger          *zap.Logger
}

func NewService(repo domain.PreprocessJobRepository, targetBucket, targetKeyPrefix string, logger *zap.Logger) (*Service, error) {
	if repo == nil {
		return nil, fmt.Errorf("job repository is required")
	}
	if logger == nil {
		logger = zap.NewNop()
	}

	return &Service{
		repo:            repo,
		targetBucket:    targetBucket,
		targetKeyPrefix: strings.TrimSuffix(targetKeyPrefix, "/"),
		logger:          logger,
	}, nil
}

func (s *Service) SubmitPreprocess(ctx context.Context, cmd SubmitCommand) (domain.PreprocessJob, error) {
	correlationID := cmd.CorrelationID
	if correlationID == "" {
		correlationID = correlationIDFromContext(ctx)
	}

	logger := s.logger.With(
		zap.String("document_id", cmd.DocumentID),
		zap.String("correlation_id", correlationID),
		zap.String("source_bucket", cmd.SourceBucket),
		zap.String("source_key", cmd.SourceKey),
	)

	targetBucket := cmd.TargetBucket
	if targetBucket == "" {
		targetBucket = s.targetBucket
		logger.Debug("using default target bucket", zap.String("target_bucket", targetBucket))
	}

	targetKey := cmd.TargetKey
	if targetKey == "" {
		targetKey = s.defaultTargetKey(cmd.DocumentID)
		logger.Debug("using default target key", zap.String("target_key", targetKey))
	}

	if targetBucket == "" || targetKey == "" {
		return domain.PreprocessJob{}, fmt.Errorf("%w: target_bucket and target_key are required", ErrInvalidRequest)
	}
	logger = logger.With(
		zap.String("target_bucket", targetBucket),
		zap.String("target_key", targetKey),
	)

	existing, found, err := s.repo.FindBySource(ctx, cmd.SourceBucket, cmd.SourceKey, cmd.SourceETag)
	if err != nil {
		logger.Error("failed to find job by source", zap.Error(err))
		return domain.PreprocessJob{}, fmt.Errorf("find job by source: %w", err)
	}
	logger.Info("checked existing job by source", zap.Bool("found", found))
	if found {
		if existing.ErrorDetails == nil {
			existing.ErrorDetails = map[string]string{}
		}

		logger.Info("returning existing preprocess job",
			zap.String("job_id", existing.ID),
			zap.String("status", existing.Status.String()),
			zap.String("target_bucket", existing.TargetBucket),
			zap.String("target_key", existing.TargetKey),
		)
		return existing, nil
	}

	now := time.Now().UTC()

	job := domain.PreprocessJob{
		ID:              uuid.NewString(),
		DocumentID:      cmd.DocumentID,
		SourceBucket:    cmd.SourceBucket,
		SourceKey:       cmd.SourceKey,
		SourceETag:      cmd.SourceETag,
		TargetBucket:    targetBucket,
		TargetKey:       targetKey,
		CallbackURL:     cmd.CallbackURL,
		CorrelationID:   correlationID,
		Status:          domain.StatusPending,
		ProcessingStage: domain.StageDownloading,
		ErrorDetails:    map[string]string{},
		Attempts:        0,
		MaxAttempts:     domain.DefaultMaxAttempts,
		NextRunAt:       now,
		CreatedAt:       now,
		UpdatedAt:       now,
		StageStartedAt:  now,
		StageUpdatedAt:  now,
	}

	if err := job.Validate(); err != nil {
		logger.Error("invalid preprocess job", zap.Error(err), zap.String("status", job.Status.String()))
		return domain.PreprocessJob{}, fmt.Errorf("%w: %v", ErrInvalidRequest, err)
	}
	logger.Debug("preprocess job validated",
		zap.String("job_id", job.ID),
		zap.String("status", job.Status.String()),
	)

	if err := s.repo.Create(ctx, job); err != nil {
		logger.Error("failed to create preprocess job",
			zap.Error(err),
			zap.String("job_id", job.ID),
			zap.String("status", job.Status.String()),
		)
		return domain.PreprocessJob{}, fmt.Errorf("create job: %w", err)
	}
	logger.Info("preprocess job created",
		zap.String("job_id", job.ID),
		zap.String("status", job.Status.String()),
		zap.String("target_bucket", job.TargetBucket),
		zap.String("target_key", job.TargetKey),
	)

	return job, nil
}

func (s *Service) GetStatus(ctx context.Context, query StatusQuery) (domain.PreprocessJob, error) {
	if query.JobID == "" {
		return domain.PreprocessJob{}, fmt.Errorf("%w: job_id is required", ErrInvalidRequest)
	}

	job, found, err := s.repo.Get(ctx, query.JobID)
	if err != nil {
		return domain.PreprocessJob{}, fmt.Errorf("get job: %w", err)
	}
	if !found {
		return domain.PreprocessJob{}, ErrJobNotFound
	}

	if job.ErrorDetails == nil {
		job.ErrorDetails = map[string]string{}
	}

	if query.CorrelationID != "" {
		job.CorrelationID = query.CorrelationID
	}

	return job, nil
}

func (s *Service) defaultTargetKey(documentID string) string {
	baseName := documentID
	if baseName == "" {
		baseName = uuid.NewString()
	}

	if s.targetKeyPrefix != "" {
		return fmt.Sprintf("%s/%s.docx", s.targetKeyPrefix, baseName)
	}

	return fmt.Sprintf("%s.docx", baseName)
}

func correlationIDFromContext(ctx context.Context) string {
	if md, ok := metadata.FromIncomingContext(ctx); ok {
		if values := md.Get("x-correlation-id"); len(values) > 0 {
			return values[0]
		}
	}

	if val := ctx.Value("X-Correlation-ID"); val != nil {
		if id, ok := val.(string); ok {
			return id
		}
	}

	return ""
}
