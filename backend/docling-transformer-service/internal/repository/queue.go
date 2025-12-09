package repository

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type TransformationStatus string

const (
	StatusPending    TransformationStatus = "pending"
	StatusProcessing TransformationStatus = "processing"
	StatusDone       TransformationStatus = "done"
	StatusFailed     TransformationStatus = "failed"
)

type TransformationTask struct {
	ID                uuid.UUID
	Status            TransformationStatus
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
}

type EnqueueParams struct {
	DocumentID        string
	OriginalBucket    string
	OriginalObjectKey string
	MarkdownBucket    string
	MarkdownObjectKey string
	FilenameExtension string
}

type QueueRepository interface {
	Enqueue(ctx context.Context, params EnqueueParams) (uuid.UUID, error)
	CountActive(ctx context.Context) (int, error)
	AcquireNext(ctx context.Context) (*TransformationTask, error)
	MarkDone(ctx context.Context, id uuid.UUID, markdownBucket, markdownObjectKey string) error
	MarkFailed(ctx context.Context, id uuid.UUID, errMsg string) error
}

type PostgresQueueRepository struct {
	pool *pgxpool.Pool
}

func NewQueueRepository(pool *pgxpool.Pool) *PostgresQueueRepository {
	return &PostgresQueueRepository{pool: pool}
}

func (r *PostgresQueueRepository) Enqueue(ctx context.Context, params EnqueueParams) (uuid.UUID, error) {
	id := uuid.New()
	normalizedExt := strings.TrimPrefix(strings.ToLower(params.FilenameExtension), ".")

	query := `
INSERT INTO transformations_queue (
    id, status, document_id, original_bucket, original_object_key, markdown_bucket, markdown_object_key, filename_extension, created_at
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, NOW())
`

	_, err := r.pool.Exec(ctx, query, id, StatusPending, params.DocumentID, params.OriginalBucket, params.OriginalObjectKey, params.MarkdownBucket, params.MarkdownObjectKey, normalizedExt)
	if err != nil {
		return uuid.Nil, fmt.Errorf("enqueue transformation: %w", err)
	}

	return id, nil
}

func (r *PostgresQueueRepository) CountActive(ctx context.Context) (int, error) {
	const query = `SELECT COUNT(*) FROM transformations_queue WHERE status IN ('pending', 'processing')`

	var count int
	if err := r.pool.QueryRow(ctx, query).Scan(&count); err != nil {
		return 0, fmt.Errorf("count active tasks: %w", err)
	}

	return count, nil
}

func (r *PostgresQueueRepository) AcquireNext(ctx context.Context) (*TransformationTask, error) {
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx)

	const selectQuery = `
SELECT id, status, document_id, original_bucket, original_object_key, markdown_bucket, markdown_object_key, filename_extension, created_at, started_at, finished_at, error
FROM transformations_queue
WHERE status = 'pending'
ORDER BY created_at
LIMIT 1
FOR UPDATE SKIP LOCKED`

	task, err := scanTask(tx.QueryRow(ctx, selectQuery))
	if err != nil {
		if err == pgx.ErrNoRows {
			if commitErr := tx.Commit(ctx); commitErr != nil {
				return nil, fmt.Errorf("commit empty tx: %w", commitErr)
			}
			return nil, nil
		}
		return nil, err
	}

	const updateQuery = `UPDATE transformations_queue SET status = 'processing', started_at = NOW() WHERE id = $1 RETURNING started_at`
	if err := tx.QueryRow(ctx, updateQuery, task.ID).Scan(&task.StartedAt); err != nil {
		return nil, fmt.Errorf("mark processing: %w", err)
	}
	task.Status = StatusProcessing

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit acquire: %w", err)
	}

	return task, nil
}

func (r *PostgresQueueRepository) MarkDone(ctx context.Context, id uuid.UUID, markdownBucket, markdownObjectKey string) error {
	const query = `UPDATE transformations_queue SET status = 'done', markdown_bucket = $1, markdown_object_key = $2, finished_at = NOW(), error = NULL WHERE id = $3`

	if _, err := r.pool.Exec(ctx, query, markdownBucket, markdownObjectKey, id); err != nil {
		return fmt.Errorf("mark done: %w", err)
	}

	return nil
}

func (r *PostgresQueueRepository) MarkFailed(ctx context.Context, id uuid.UUID, errMsg string) error {
	const query = `UPDATE transformations_queue SET status = 'failed', finished_at = NOW(), error = $1 WHERE id = $2`

	if _, err := r.pool.Exec(ctx, query, errMsg, id); err != nil {
		return fmt.Errorf("mark failed: %w", err)
	}

	return nil
}

func scanTask(row pgx.Row) (*TransformationTask, error) {
	var task TransformationTask
	if err := row.Scan(
		&task.ID,
		&task.Status,
		&task.DocumentID,
		&task.OriginalBucket,
		&task.OriginalObjectKey,
		&task.MarkdownBucket,
		&task.MarkdownObjectKey,
		&task.FilenameExtension,
		&task.CreatedAt,
		&task.StartedAt,
		&task.FinishedAt,
		&task.Error,
	); err != nil {
		if err == pgx.ErrNoRows {
			return nil, pgx.ErrNoRows
		}

		return nil, fmt.Errorf("scan task: %w", err)
	}

	return &task, nil
}
