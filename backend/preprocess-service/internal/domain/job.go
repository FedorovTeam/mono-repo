package domain

import (
	"fmt"
	"time"

	preprocessv1 "preprocess-service/proto/backend/preprocess/v1"
)

type PreprocessJob struct {
	ID            string
	DocumentID    string
	SourceBucket  string
	SourceKey     string
	SourceETag    string
	TargetBucket  string
	TargetKey     string
	CallbackURL   string
	CorrelationID string
	Status        preprocessv1.PreprocessStatus
	ErrorMessage  string
	ErrorCode     string
	ErrorDetails  map[string]string
	UpdatedAt     time.Time
}

func (j PreprocessJob) Validate() error {
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
	return nil
}
