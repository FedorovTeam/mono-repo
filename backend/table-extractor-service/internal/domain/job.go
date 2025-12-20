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
	ID              uuid.UUID
	DocumentID      string
	SourceURL       string
	LocalPath       string
	Original        ArtifactLocation
	ArtifactsBucket string
	ArtifactsPrefix string
	Artifacts       TableArtifacts
	Status          TableExtractionStatus
	StatusReason    string
	Attempts        int
	NextRunAt       *time.Time
	FinishedAt      *time.Time
	ErrorCode       string
	ErrorMessage    string
	CorrelationID   string
	CreatedAt       time.Time
	UpdatedAt       time.Time
}
