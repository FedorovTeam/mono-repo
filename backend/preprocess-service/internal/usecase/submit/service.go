package submit

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

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
	repo            domain.JobRepository
	targetBucket    string
	targetKeyPrefix string
}

func NewService(repo domain.JobRepository, targetBucket, targetKeyPrefix string) (*Service, error) {
	if repo == nil {
		return nil, fmt.Errorf("job repository is required")
	}

	return &Service{
		repo:            repo,
		targetBucket:    targetBucket,
		targetKeyPrefix: strings.TrimSuffix(targetKeyPrefix, "/"),
	}, nil
}

func (s *Service) SubmitPreprocess(ctx context.Context, cmd SubmitCommand) (domain.PreprocessJob, error) {
	targetBucket := cmd.TargetBucket
	if targetBucket == "" {
		targetBucket = s.targetBucket
	}

	targetKey := cmd.TargetKey
	if targetKey == "" {
		targetKey = s.defaultTargetKey(cmd.DocumentID)
	}

	if targetBucket == "" || targetKey == "" {
		return domain.PreprocessJob{}, fmt.Errorf("%w: target_bucket and target_key are required", ErrInvalidRequest)
	}

	existing, found, err := s.repo.FindBySource(ctx, cmd.SourceBucket, cmd.SourceKey, cmd.SourceETag)
	if err != nil {
		return domain.PreprocessJob{}, fmt.Errorf("find job by source: %w", err)
	}
	if found {
		if existing.ErrorDetails == nil {
			existing.ErrorDetails = map[string]string{}
		}
		return existing, nil
	}

	job := domain.PreprocessJob{
		ID:            uuid.NewString(),
		DocumentID:    cmd.DocumentID,
		SourceBucket:  cmd.SourceBucket,
		SourceKey:     cmd.SourceKey,
		SourceETag:    cmd.SourceETag,
		TargetBucket:  targetBucket,
		TargetKey:     targetKey,
		CallbackURL:   cmd.CallbackURL,
		CorrelationID: cmd.CorrelationID,
		Status:        domain.StatusPending,
		ErrorDetails:  map[string]string{},
		UpdatedAt:     time.Now().UTC(),
	}

	if err := job.Validate(); err != nil {
		return domain.PreprocessJob{}, fmt.Errorf("%w: %v", ErrInvalidRequest, err)
	}

	if err := s.repo.Save(ctx, job); err != nil {
		return domain.PreprocessJob{}, fmt.Errorf("save job: %w", err)
	}

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
