package domain

import (
	"time"

	"github.com/google/uuid"
)

type TableExtractionStatus string

const (
	StatusPending         TableExtractionStatus = "pending"
	StatusProcessing      TableExtractionStatus = "processing"
	StatusDone            TableExtractionStatus = "done"
	StatusFailed          TableExtractionStatus = "failed"
	StatusFailedRetryable TableExtractionStatus = "failed_retryable"
)

type ArtifactLocation struct {
	Bucket    string
	ObjectKey string
}

type TableArtifacts struct {
	RawJSON        ArtifactLocation
	NormalizedJSON ArtifactLocation
	Markdown       ArtifactLocation
	Manifest       ArtifactLocation
	DebugPrefix    ArtifactLocation
}

type TableExtractionJob struct {
	ID            uuid.UUID
	DocumentID    string
	Status        TableExtractionStatus
	StatusReason  string
	Artifacts     TableArtifacts
	CorrelationID string
	CreatedAt     time.Time
	UpdatedAt     time.Time
}
