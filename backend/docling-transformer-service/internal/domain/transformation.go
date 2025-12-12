package domain

import (
	"context"
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
	MarkdownBucket    string
	MarkdownObjectKey string
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
	MarkdownBucket    string
	MarkdownObjectKey string
	FilenameExtension string
	CorrelationID     string
}

type QueueRepository interface {
	Enqueue(ctx context.Context, params EnqueueParams) (uuid.UUID, error)
	CountActive(ctx context.Context) (int, error)
	AcquireNext(ctx context.Context) (*TransformationTask, error)
	AcquireBatch(ctx context.Context, limit int) ([]*TransformationTask, error)
	GetLatestByDocument(ctx context.Context, documentID string) (*TransformationTask, error)
	GetByID(ctx context.Context, id uuid.UUID) (*TransformationTask, error)
	MarkDone(ctx context.Context, id uuid.UUID, markdownBucket, markdownObjectKey string) error
	MarkFailed(ctx context.Context, id uuid.UUID, errMsg string) error
	UpdateDoclingInfo(ctx context.Context, id uuid.UUID, jobID string, pollCount int) error
	MarkDoclingWaiting(ctx context.Context, id uuid.UUID, jobID string, pollCount int, nextPollAt time.Time) error
	ActivateWaiting(ctx context.Context, now time.Time, maxDoclingLifetime time.Duration) (int, int, error)
	RestoreProcessing(ctx context.Context) (int, error)
	RestoreStuckProcessing(ctx context.Context, timeout time.Duration, maxRetries, maxDoclingPollAttempts int) (restored, failed int, err error)
	CleanupFinished(ctx context.Context, retention time.Duration) (int, error)
	CountByStatus(ctx context.Context) (pending, processing, waitingDocling int, err error)
}
