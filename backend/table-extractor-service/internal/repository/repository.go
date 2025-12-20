package repository

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"

	"github.com/mono-repo/table-extractor-service/internal/domain"
)

var ErrQueueLimitReached = errors.New("queue limit reached")

type TableExtractionRepository interface {
	UpsertOrGetActive(ctx context.Context, job domain.TableExtractionJob, queueLimit int) (domain.TableExtractionJob, bool, error)
	AcquirePendingBatch(ctx context.Context, limit int, now time.Time) ([]domain.TableExtractionJob, error)
	UpdateStatus(ctx context.Context, id uuid.UUID, status domain.TableExtractionStatus, statusReason, errorCode, errorMessage string, finishedAt *time.Time) error
	Postpone(ctx context.Context, id uuid.UUID, nextRunAt time.Time, statusReason string) error
	ListProcessingBefore(ctx context.Context, updatedBefore time.Time) ([]domain.TableExtractionJob, error)
	Cleanup(ctx context.Context, finishedBefore time.Time) (int, error)
	RetryFailed(ctx context.Context, maxAttempts int, retryDelay time.Duration, now time.Time) (int, error)
	GetByID(ctx context.Context, id uuid.UUID) (domain.TableExtractionJob, bool, error)
	CountActive(ctx context.Context) (int, error)
}
