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
	RetryCount        int
	DoclingJobID      string
	DoclingPollCount  int
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
	MarkDone(ctx context.Context, id uuid.UUID, markdownBucket, markdownObjectKey string) error
	MarkFailed(ctx context.Context, id uuid.UUID, errMsg string) error
	UpdateDoclingInfo(ctx context.Context, id uuid.UUID, jobID string, pollCount int) error
	RestoreStuckProcessing(ctx context.Context, timeout time.Duration, maxRetries, maxDoclingPollAttempts int) (restored, failed int, err error)
	CleanupFinished(ctx context.Context, retention time.Duration) (int, error)
	CountByStatus(ctx context.Context) (pending, processing, waitingDocling int, err error)
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
    id, status, document_id, original_bucket, original_object_key, markdown_bucket, markdown_object_key, filename_extension, created_at, correlation_id, docling_poll_count
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, NOW(), $9, 0)
`

	_, err := r.pool.Exec(ctx, query, id, StatusPending, params.DocumentID, params.OriginalBucket, params.OriginalObjectKey, params.MarkdownBucket, params.MarkdownObjectKey, normalizedExt, params.CorrelationID)
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

func (r *PostgresQueueRepository) CountByStatus(ctx context.Context) (pending, processing, waitingDocling int, err error) {
	const query = `
SELECT
    COUNT(*) FILTER (WHERE status = 'pending') AS pending,
    COUNT(*) FILTER (WHERE status = 'processing' AND (docling_job_id IS NULL OR docling_job_id = '')) AS processing,
    COUNT(*) FILTER (WHERE status = 'processing' AND (docling_job_id IS NOT NULL AND docling_job_id <> '')) AS waiting_docling
FROM transformations_queue`

	if err := r.pool.QueryRow(ctx, query).Scan(&pending, &processing, &waitingDocling); err != nil {
		return 0, 0, 0, fmt.Errorf("count by status: %w", err)
	}

	return pending, processing, waitingDocling, nil
}

func (r *PostgresQueueRepository) AcquireNext(ctx context.Context) (*TransformationTask, error) {
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx)

	const selectQuery = `
SELECT id, status, retry_count, docling_job_id, docling_poll_count, document_id, original_bucket, original_object_key, markdown_bucket, markdown_object_key, filename_extension, created_at, started_at, finished_at, error, correlation_id
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

	const updateQuery = `UPDATE transformations_queue SET status = 'processing', started_at = NOW(), retry_count = retry_count + 1 WHERE id = $1 RETURNING started_at, retry_count`
	if err := tx.QueryRow(ctx, updateQuery, task.ID).Scan(&task.StartedAt, &task.RetryCount); err != nil {
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

func (r *PostgresQueueRepository) UpdateDoclingInfo(ctx context.Context, id uuid.UUID, jobID string, pollCount int) error {
	if pollCount < 0 {
		pollCount = 0
	}

	const query = `UPDATE transformations_queue SET docling_job_id = COALESCE(NULLIF($2, ''), docling_job_id), docling_poll_count = $3 WHERE id = $1`

	if _, err := r.pool.Exec(ctx, query, id, jobID, pollCount); err != nil {
		return fmt.Errorf("update docling info: %w", err)
	}

	return nil
}

func (r *PostgresQueueRepository) RestoreStuckProcessing(ctx context.Context, timeout time.Duration, maxRetries, maxDoclingPollAttempts int) (restored, failed int, err error) {
	const query = `
WITH stalled AS (
    SELECT id, retry_count, docling_job_id, docling_poll_count FROM transformations_queue
    WHERE status = 'processing' AND started_at < NOW() - $1::interval
),
failed AS (
    UPDATE transformations_queue
    SET status = 'failed', finished_at = NOW(),
        error = CASE
            WHEN docling_job_id IS NOT NULL AND docling_job_id <> '' AND $3 > 0 AND docling_poll_count >= $3 THEN 'docling polling attempts exceeded'
            ELSE 'processing timeout exceeded'
        END
    WHERE id IN (
        SELECT id FROM stalled
        WHERE retry_count >= $2
           OR (docling_job_id IS NOT NULL AND docling_job_id <> '' AND $3 > 0 AND docling_poll_count >= $3)
    )
    RETURNING id
),
restored AS (
    UPDATE transformations_queue
    SET status = 'pending', started_at = NULL, error = 'restored after timeout',
        docling_job_id = NULL, docling_poll_count = 0
    WHERE id IN (SELECT id FROM stalled EXCEPT SELECT id FROM failed)
    RETURNING id
)
SELECT (SELECT COUNT(*) FROM restored), (SELECT COUNT(*) FROM failed);`

	if maxRetries <= 0 {
		maxRetries = 1
	}

	if maxDoclingPollAttempts < 0 {
		maxDoclingPollAttempts = 0
	}

	if err := r.pool.QueryRow(ctx, query, timeout, maxRetries, maxDoclingPollAttempts).Scan(&restored, &failed); err != nil {
		return 0, 0, fmt.Errorf("restore stuck tasks: %w", err)
	}

	return restored, failed, nil
}

func (r *PostgresQueueRepository) CleanupFinished(ctx context.Context, retention time.Duration) (int, error) {
	const query = `DELETE FROM transformations_queue WHERE status IN ('done', 'failed') AND finished_at < NOW() - $1::interval RETURNING 1`

	rows, err := r.pool.Query(ctx, query, retention)
	if err != nil {
		return 0, fmt.Errorf("cleanup finished: %w", err)
	}
	defer rows.Close()

	count := 0
	for rows.Next() {
		count++
	}

	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("iterate cleanup results: %w", err)
	}

	return count, nil
}

func scanTask(row pgx.Row) (*TransformationTask, error) {
	var task TransformationTask
	if err := row.Scan(
		&task.ID,
		&task.Status,
		&task.RetryCount,
		&task.DoclingJobID,
		&task.DoclingPollCount,
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
		&task.CorrelationID,
	); err != nil {
		if err == pgx.ErrNoRows {
			return nil, pgx.ErrNoRows
		}

		return nil, fmt.Errorf("scan task: %w", err)
	}

	return &task, nil
}
