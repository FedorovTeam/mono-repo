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
	ID                uuid.UUID
	Status            TransformationStatus
	RetryCount        int
	DoclingJobID      string
	DoclingPollCount  int
	NextPollAt        *time.Time
	DocumentID        string
	OriginalBucket    string
	OriginalObjectKey string
	SourceBucket      string
	SourceObjectKey   string
	SourceURL         string
	SourceEndpoint    string
	SourceAccessKey   string
	SourceSecretKey   string
	SourceUseSSL      *bool
	SourceRegion      string
	MarkdownBucket    string
	MarkdownObjectKey string
	TargetEndpoint    string
	TargetAccessKey   string
	TargetSecretKey   string
	TargetUseSSL      *bool
	TargetRegion      string
	FilenameExtension string
	CreatedAt         time.Time
	StartedAt         *time.Time
	FinishedAt        *time.Time
	Error             *string
	CorrelationID     string
}

type EnqueueParams struct {
	DocumentID        string
	OriginalBucket    string
	OriginalObjectKey string
	SourceBucket      string
	SourceObjectKey   string
	SourceURL         string
	SourceEndpoint    string
	SourceAccessKey   string
	SourceSecretKey   string
	SourceUseSSL      *bool
	SourceRegion      string
	MarkdownBucket    string
	MarkdownObjectKey string
	TargetEndpoint    string
	TargetAccessKey   string
	TargetSecretKey   string
	TargetUseSSL      *bool
	TargetRegion      string
	FilenameExtension string
	CorrelationID     string
}
