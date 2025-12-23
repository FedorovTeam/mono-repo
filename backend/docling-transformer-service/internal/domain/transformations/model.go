// backend/docling-transformer-service/internal/domain/transformations/model.go
package transformations

import (
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/mono-repo/docling-transformer-service/internal/domain"
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

type DoclingRestartsExceededError struct {
	Restarts    int
	MaxRestarts int
}

func (e *DoclingRestartsExceededError) Error() string {
	switch {
	case e == nil:
		return ""
	case e.MaxRestarts > 0:
		return fmt.Sprintf("docling restarts exceeded: %d/%d", e.Restarts, e.MaxRestarts)
	default:
		return fmt.Sprintf("docling restarts exceeded: %d", e.Restarts)
	}
}

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

	TableCount    int
	TablePages    []int32
	TableMetadata *domain.TableMetadata
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

	TableMetadata *domain.TableMetadata
}
