package domain

import (
	"fmt"
	"time"
)

type PreprocessJob struct {
	ID              string
	DocumentID      string
	SourceBucket    string
	SourceKey       string
	SourceETag      string
	TargetBucket    string
	TargetKey       string
	TargetMediaType string
	CallbackURL     string
	CorrelationID   string
	Status          PreprocessStatus
	ProcessingStage ProcessingStage
	Attempts        int
	MaxAttempts     int
	NextRunAt       time.Time
	StartedAt       time.Time
	FinishedAt      time.Time
	StageStartedAt  time.Time
	StageUpdatedAt  time.Time
	ErrorMessage    string
	ErrorCode       string
	ErrorDetails    map[string]string
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

const DefaultMaxAttempts = 5

type Failure struct {
	Code      ErrorCode
	Message   string
	Permanent bool
	Details   map[string]string
}

type FailureUpdate struct {
	Status       PreprocessStatus
	NextRunAt    time.Time
	Code         ErrorCode
	Message      string
	Permanent    bool
	TargetBucket string
	TargetKey    string
	Details      map[string]string
}

func (j PreprocessJob) Validate() error {
	if j.ID == "" {
		return fmt.Errorf("job id is required")
	}
	if j.DocumentID == "" {
		return fmt.Errorf("document id is required")
	}
	if j.SourceBucket == "" {
		return fmt.Errorf("source bucket is required")
	}
	if j.SourceKey == "" {
		return fmt.Errorf("source key is required")
	}
	if j.SourceETag == "" {
		return fmt.Errorf("source etag is required")
	}
	if j.Status == StatusUnspecified {
		return fmt.Errorf("status is required")
	}
	if !j.ProcessingStage.IsValid() {
		return fmt.Errorf("processing stage is required")
	}
	if j.Attempts < 0 {
		return fmt.Errorf("attempts cannot be negative")
	}
	if j.MaxAttempts <= 0 {
		return fmt.Errorf("max attempts must be positive")
	}
	if j.NextRunAt.IsZero() {
		return fmt.Errorf("next run time is required")
	}
	if j.StageUpdatedAt.IsZero() {
		return fmt.Errorf("stage update time is required")
	}
	return nil
}

func (j PreprocessJob) MarkProcessing(now time.Time) (PreprocessJob, error) {
	j.Attempts++
	j.Status = StatusProcessing
	j.StartedAt = now
	j.ProcessingStage = StageDownloading
	j.StageStartedAt = now
	j.StageUpdatedAt = now
	j.UpdatedAt = now

	if j.NextRunAt.IsZero() {
		j.NextRunAt = now
	}

	return j, j.Validate()
}

func (j PreprocessJob) MarkCompleted(now time.Time) (PreprocessJob, error) {
	j.Status = StatusCompleted
	j.FinishedAt = now
	if j.ProcessingStage == StageUnspecified {
		j.ProcessingStage = StageCallback
	}
	if j.StageUpdatedAt.IsZero() {
		j.StageUpdatedAt = now
	}
	j.UpdatedAt = now

	return j, j.Validate()
}

func (j PreprocessJob) MarkFailed(now time.Time, policy RetryPolicy, failure Failure) (PreprocessJob, FailureUpdate, error) {
	failure = failure.normalize()

	j.ErrorCode = failure.Code.String()
	j.ErrorMessage = failure.Message
	j.ErrorDetails = failure.Details
	if j.StageUpdatedAt.IsZero() {
		j.StageUpdatedAt = now
	}
	j.UpdatedAt = now

	if policy.ShouldStop(j.Attempts, j.MaxAttempts, failure.Permanent) {
		j.Status = StatusStopped
		j.FinishedAt = now
		j.NextRunAt = now
	} else {
		j.Status = StatusFailed
		j.NextRunAt = policy.NextRun(now, j.Attempts)
	}

	if err := j.Validate(); err != nil {
		return PreprocessJob{}, FailureUpdate{}, fmt.Errorf("validate job %s: %w", j.ID, err)
	}

	update := FailureUpdate{
		Status:       j.Status,
		NextRunAt:    j.NextRunAt,
		Code:         failure.Code,
		Message:      failure.Message,
		Permanent:    failure.Permanent,
		TargetBucket: j.TargetBucket,
		TargetKey:    j.TargetKey,
		Details:      failure.Details,
	}

	return j, update, nil
}

func (f Failure) normalize() Failure {
	if f.Code == "" {
		f.Code = ErrorCodeProcessingError
	}
	if f.Message == "" {
		f.Message = f.Code.String()
	}
	if f.Details == nil {
		f.Details = map[string]string{}
	}
	return f
}
