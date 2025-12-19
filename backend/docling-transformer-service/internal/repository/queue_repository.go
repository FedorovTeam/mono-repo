// backend/docling-transformer-service/internal/repository/queue_repository.go
package repository

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/mono-repo/docling-transformer-service/internal/domain/transformations"
)

type QueueRepository interface {
	Enqueue(ctx context.Context, params transformations.EnqueueParams) (uuid.UUID, error)

	AcquireNext(ctx context.Context) (*transformations.TransformationTask, error)
	AcquireBatch(ctx context.Context, limit int) ([]*transformations.TransformationTask, error)
	AcquireSubmitLock(ctx context.Context) (SubmitLock, error)

	GetLatestByDocument(ctx context.Context, documentID string) (*transformations.TransformationTask, error)
	GetByID(ctx context.Context, id uuid.UUID) (*transformations.TransformationTask, error)

	MarkDone(ctx context.Context, id uuid.UUID, markdownBucket, markdownObjectKey string) error
	MarkFailed(ctx context.Context, id uuid.UUID, errMsg string) error

	UpdateDoclingInfo(ctx context.Context, id uuid.UUID, taskID, jobID string, pollCount int) error
	MarkDoclingWaiting(ctx context.Context, id uuid.UUID, taskID, jobID string, pollCount int, nextPollAt time.Time) error
	RequeueDoclingWaiting(ctx context.Context, id uuid.UUID, nextPollAt *time.Time) error

	ActivateWaiting(ctx context.Context, now time.Time, maxDoclingLifetime time.Duration) (int, int, error)
	RestoreProcessing(ctx context.Context) (int, error)
	RestoreStuckProcessing(ctx context.Context, timeout time.Duration, maxRetries, maxDoclingPollAttempts int) (restored, failed int, err error)
	RetryFailed(ctx context.Context, olderThan time.Time, maxRetries int) (retried, exhausted int, err error)
	CleanupFinished(ctx context.Context, retention time.Duration) (int, error)
	CountByStatus(ctx context.Context) (pending, processing, waitingDocling int, err error)
	ReschedulePending(ctx context.Context, id uuid.UUID, nextPollAt time.Time) error
}

type SubmitLock interface {
	Release(ctx context.Context) error
}

var ErrSubmitLockBusy = errors.New("docling submit lock is busy")
