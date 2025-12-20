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
	Attempts        int
	MaxAttempts     int
	NextRunAt       time.Time
	StartedAt       time.Time
	FinishedAt      time.Time
	ErrorMessage    string
	ErrorCode       string
	ErrorDetails    map[string]string
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

const DefaultMaxAttempts = 5

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
	if j.Attempts < 0 {
		return fmt.Errorf("attempts cannot be negative")
	}
	if j.MaxAttempts <= 0 {
		return fmt.Errorf("max attempts must be positive")
	}
	if j.NextRunAt.IsZero() {
		return fmt.Errorf("next run time is required")
	}
	return nil
}
