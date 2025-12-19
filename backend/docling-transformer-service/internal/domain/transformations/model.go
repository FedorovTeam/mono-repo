// backend/docling-transformer-service/internal/domain/transformations/model.go
package transformations

import (
	"errors"
	"time"

	"github.com/google/uuid"
)

type TransformationStatus string

const (
	StatusPending    TransformationStatus = "pending"
	StatusProcessing TransformationStatus = "processing"
	StatusDocling    TransformationStatus = "waiting_docling"
	StatusDone       TransformationStatus = "done"
	StatusFailed     TransformationStatus = "failed"
)

var ErrPollingAttemptsExceeded = errors.New("conversion polling attempts exceeded")

type TransformationTask struct {
	ID               uuid.UUID
	Status           TransformationStatus
	RetryCount       int
	DoclingTaskID    string
	DoclingJobID     string
	DoclingPollCount int
	NextPollAt       *time.Time

	DocumentID        string
	OriginalBucket    string
	OriginalObjectKey string

	SourceBucket    string
	SourceObjectKey string
	SourceURL       string

	MarkdownBucket    string
	MarkdownObjectKey string
	FilenameExtension string

	CorrelationID string

	CreatedAt  time.Time
	StartedAt  *time.Time
	FinishedAt *time.Time
	Error      *string
}

type EnqueueParams struct {
	DocumentID        string
	OriginalBucket    string
	OriginalObjectKey string

	SourceBucket    string
	SourceObjectKey string
	SourceURL       string

	MarkdownBucket    string
	MarkdownObjectKey string
	FilenameExtension string

	CorrelationID string
}
