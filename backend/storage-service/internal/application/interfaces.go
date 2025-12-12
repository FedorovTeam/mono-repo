package application

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"backend-storage-service/internal/domain"
)

// ErrNoJobs сигнализирует об отсутствии задач для обработки.
var ErrNoJobs = pgx.ErrNoRows

// Tx описывает контракт для транзакции.
type Tx interface {
	Exec(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Commit(ctx context.Context) error
	Rollback(ctx context.Context) error
}

// TxManager отвечает за создание транзакций.
type TxManager interface {
	Begin(ctx context.Context) (Tx, error)
}

// ProcessingJobRepository инкапсулирует операции с задачами обработки.
type ProcessingJobRepository interface {
	SelectPendingForStage(ctx context.Context, tx Tx, stage string) (*domain.ProcessingJob, error)
	MarkInProgress(ctx context.Context, tx Tx, jobID, stage, message string) error
	MarkCompleted(ctx context.Context, tx Tx, jobID, stage, message string) error
	RescheduleOrFail(ctx context.Context, tx Tx, job *domain.ProcessingJob, stage string, reason error, maxAttempts int, backoff time.Duration) error
}

// DocumentRepository описывает операции над документами.
type DocumentRepository interface {
	MarkStatus(ctx context.Context, tx Tx, documentID string, status domain.DocumentStatus, payload map[string]any, reason string) error
}
