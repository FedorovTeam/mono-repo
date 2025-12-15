package repository

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/mono-repo/docling-transformer-service/internal/domain/transformations"
)

type TransformationDTO struct {
	ID                uuid.UUID
	Status            transformations.TransformationStatus
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
	MarkdownBucket    string
	MarkdownObjectKey string
	FilenameExtension string
	CreatedAt         time.Time
	StartedAt         *time.Time
	FinishedAt        *time.Time
	Error             *string
	CorrelationID     string
}

func (dto TransformationDTO) ToDomain() *transformations.TransformationTask {
	return &transformations.TransformationTask{
		ID:                dto.ID,
		Status:            dto.Status,
		RetryCount:        dto.RetryCount,
		DoclingJobID:      dto.DoclingJobID,
		DoclingPollCount:  dto.DoclingPollCount,
		NextPollAt:        dto.NextPollAt,
		DocumentID:        dto.DocumentID,
		OriginalBucket:    dto.OriginalBucket,
		OriginalObjectKey: dto.OriginalObjectKey,
		SourceBucket:      dto.SourceBucket,
		SourceObjectKey:   dto.SourceObjectKey,
		SourceURL:         dto.SourceURL,
		MarkdownBucket:    dto.MarkdownBucket,
		MarkdownObjectKey: dto.MarkdownObjectKey,
		FilenameExtension: dto.FilenameExtension,
		CreatedAt:         dto.CreatedAt,
		StartedAt:         dto.StartedAt,
		FinishedAt:        dto.FinishedAt,
		Error:             dto.Error,
		CorrelationID:     dto.CorrelationID,
	}
}

type QueueRepository interface {
	Enqueue(ctx context.Context, params transformations.EnqueueParams) (uuid.UUID, error)
	AcquireNext(ctx context.Context) (*transformations.TransformationTask, error)
	AcquireBatch(ctx context.Context, limit int) ([]*transformations.TransformationTask, error)
	GetLatestByDocument(ctx context.Context, documentID string) (*transformations.TransformationTask, error)
	GetByID(ctx context.Context, id uuid.UUID) (*transformations.TransformationTask, error)
	MarkDone(ctx context.Context, id uuid.UUID, markdownBucket, markdownObjectKey string) error
	MarkFailed(ctx context.Context, id uuid.UUID, errMsg string) error
	UpdateDoclingInfo(ctx context.Context, id uuid.UUID, jobID string, pollCount int) error
	MarkDoclingWaiting(ctx context.Context, id uuid.UUID, jobID string, pollCount int, nextPollAt time.Time) error
	ActivateWaiting(ctx context.Context, now time.Time, maxDoclingLifetime time.Duration) (int, int, error)
	RestoreProcessing(ctx context.Context) (int, error)
	RestoreStuckProcessing(ctx context.Context, timeout time.Duration, maxRetries, maxDoclingPollAttempts int) (restored, failed int, err error)
	RetryFailed(ctx context.Context, olderThan time.Time, maxRetries int) (retried, exhausted int, err error)
	CleanupFinished(ctx context.Context, retention time.Duration) (int, error)
	CountByStatus(ctx context.Context) (pending, processing, waitingDocling int, err error)
}
