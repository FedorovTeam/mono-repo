package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	transformations "github.com/mono-repo/docling-transformer-service/internal/domain/transformations"
	repo "github.com/mono-repo/docling-transformer-service/internal/repository"
)

const (
	StatusPending    = transformations.StatusPending
	StatusProcessing = transformations.StatusProcessing
	StatusDocling    = transformations.StatusDocling
	StatusDone       = transformations.StatusDone
	StatusFailed     = transformations.StatusFailed
)

type TransformationTask = transformations.TransformationTask
type EnqueueParams = transformations.EnqueueParams

var _ repo.QueueRepository = (*PostgresQueueRepository)(nil)

type PostgresQueueRepository struct {
	pool *pgxpool.Pool
}

func NewQueueRepository(pool *pgxpool.Pool) *PostgresQueueRepository {
	return &PostgresQueueRepository{pool: pool}
}

// GetLatestByDocument возвращает самую свежую задачу для документа, если она есть.
// При отсутствии записей возвращает (nil, nil).
func (r *PostgresQueueRepository) GetLatestByDocument(ctx context.Context, documentID string) (*TransformationTask, error) {
	const query = `
SELECT id, status, retry_count, docling_job_id, docling_poll_count, next_poll_at, document_id, original_bucket, original_object_key, source_bucket, source_object_key, source_url, markdown_bucket, markdown_object_key, filename_extension, created_at, started_at, finished_at, error, correlation_id
FROM transformations_queue
WHERE document_id = $1
ORDER BY created_at DESC
LIMIT 1`

	row := r.pool.QueryRow(ctx, query, documentID)

	task, err := scanTask(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get latest by document: %w", err)
	}

	return task, nil
}

// GetByID возвращает задачу очереди по идентификатору.
func (r *PostgresQueueRepository) GetByID(ctx context.Context, id uuid.UUID) (*TransformationTask, error) {
	const query = `
SELECT id, status, retry_count, docling_job_id, docling_poll_count, next_poll_at, document_id, original_bucket, original_object_key, source_bucket, source_object_key, source_url, markdown_bucket, markdown_object_key, filename_extension, created_at, started_at, finished_at, error, correlation_id
FROM transformations_queue
WHERE id = $1`

	row := r.pool.QueryRow(ctx, query, id)

	task, err := scanTask(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get task by id: %w", err)
	}

	return task, nil
}

func (r *PostgresQueueRepository) Enqueue(ctx context.Context, params EnqueueParams) (uuid.UUID, error) {
	id := uuid.New()
	normalizedExt := strings.TrimPrefix(strings.ToLower(params.FilenameExtension), ".")

	query := `
INSERT INTO transformations_queue (
 id, status, document_id, original_bucket, original_object_key, source_bucket, source_object_key, source_url, markdown_bucket, markdown_object_key, filename_extension, created_at, correlation_id, docling_poll_count, next_poll_at
 ) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, NOW(), $12, 0, NULL)
`

	_, err := r.pool.Exec(ctx, query,
		id, StatusPending, params.DocumentID, params.OriginalBucket, params.OriginalObjectKey,
		params.SourceBucket, params.SourceObjectKey, params.SourceURL, params.MarkdownBucket, params.MarkdownObjectKey,
		normalizedExt, params.CorrelationID,
	)
	if err != nil {
		return uuid.Nil, fmt.Errorf("enqueue transformation: %w", err)
	}

	return id, nil
}

func (r *PostgresQueueRepository) CountByStatus(ctx context.Context) (pending, processing, waitingDocling int, err error) {
	const query = `
SELECT
COUNT(*) FILTER (WHERE status = 'pending') AS pending,
COUNT(*) FILTER (WHERE status = 'processing') AS processing,
COUNT(*) FILTER (WHERE status = 'waiting_docling') AS waiting_docling
FROM transformations_queue`

	if err := r.pool.QueryRow(ctx, query).Scan(&pending, &processing, &waitingDocling); err != nil {
		return 0, 0, 0, fmt.Errorf("count by status: %w", err)
	}

	return pending, processing, waitingDocling, nil
}

func (r *PostgresQueueRepository) AcquireNext(ctx context.Context) (*TransformationTask, error) {
	tasks, err := r.AcquireBatch(ctx, 1)
	if err != nil || len(tasks) == 0 {
		return nil, err
	}

	return tasks[0], nil
}

// AcquireBatch выбирает несколько задач из очереди и помечает их как processing в одной транзакции.
func (r *PostgresQueueRepository) AcquireBatch(ctx context.Context, limit int) ([]*TransformationTask, error) {
	if limit <= 0 {
		limit = 1
	}

	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx)

	tasks, err := r.selectNextTasks(ctx, tx, limit)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			if commitErr := tx.Commit(ctx); commitErr != nil {
				return nil, fmt.Errorf("commit empty tx: %w", commitErr)
			}
			return nil, nil
		}

		return nil, err
	}

	for _, task := range tasks {
		if err := r.markProcessing(ctx, tx, task); err != nil {
			return nil, err
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit acquire: %w", err)
	}

	return tasks, nil
}

func (r *PostgresQueueRepository) selectNextTasks(ctx context.Context, tx pgx.Tx, limit int) ([]*TransformationTask, error) {
	tasks, err := r.fetchTasks(ctx, tx, `
SELECT id, status, retry_count, docling_job_id, docling_poll_count, next_poll_at, document_id, original_bucket, original_object_key, source_bucket, source_object_key, source_url, markdown_bucket, markdown_object_key, filename_extension, created_at, started_at, finished_at, error, correlation_id
FROM transformations_queue
WHERE status = 'waiting_docling' AND (next_poll_at IS NULL OR next_poll_at <= NOW())
ORDER BY COALESCE(next_poll_at, created_at), created_at
LIMIT $1
FOR UPDATE SKIP LOCKED`, 1)
	if err == nil && len(tasks) > 0 {
		return tasks, nil
	}
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}

	hasWaiting, err := r.hasWaitingDocling(ctx, tx)
	if err != nil {
		return nil, err
	}
	if hasWaiting {
		return nil, pgx.ErrNoRows
	}

	tasks, err = r.fetchTasks(ctx, tx, `
SELECT id, status, retry_count, docling_job_id, docling_poll_count, next_poll_at, document_id, original_bucket, original_object_key, source_bucket, source_object_key, source_url, markdown_bucket, markdown_object_key, filename_extension, created_at, started_at, finished_at, error, correlation_id
FROM transformations_queue
WHERE status = 'pending'
ORDER BY created_at
LIMIT $1
FOR UPDATE SKIP LOCKED`, limit)
	if err != nil {
		return nil, err
	}

	return tasks, nil
}

func (r *PostgresQueueRepository) hasWaitingDocling(ctx context.Context, tx pgx.Tx) (bool, error) {
	const query = `SELECT 1 FROM transformations_queue WHERE status = 'waiting_docling' LIMIT 1`

	var marker int
	switch err := tx.QueryRow(ctx, query).Scan(&marker); {
	case err == nil:
		return true, nil
	case errors.Is(err, pgx.ErrNoRows):
		return false, nil
	default:
		return false, fmt.Errorf("check waiting docling: %w", err)
	}
}

func (r *PostgresQueueRepository) fetchTasks(ctx context.Context, tx pgx.Tx, query string, limit int) ([]*TransformationTask, error) {
	rows, err := tx.Query(ctx, query, limit)
	if err != nil {
		return nil, fmt.Errorf("select tasks: %w", err)
	}
	defer rows.Close()

	var tasks []*TransformationTask
	for rows.Next() {
		task, scanErr := scanTask(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		tasks = append(tasks, task)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	if len(tasks) == 0 {
		return nil, pgx.ErrNoRows
	}

	return tasks, nil
}

func (r *PostgresQueueRepository) markProcessing(ctx context.Context, tx pgx.Tx, task *TransformationTask) error {
	if task == nil {
		return fmt.Errorf("nil task")
	}

	const pendingUpdateQuery = `UPDATE transformations_queue SET status = 'processing', started_at = NOW(), retry_count = retry_count + 1, next_poll_at = NULL WHERE id = $1 RETURNING started_at, retry_count`
	const waitingUpdateQuery = `UPDATE transformations_queue SET status = 'processing', started_at = NOW(), next_poll_at = NULL WHERE id = $1 RETURNING started_at, retry_count`

	query := pendingUpdateQuery
	if task.Status == StatusDocling {
		query = waitingUpdateQuery
	}

	if err := tx.QueryRow(ctx, query, task.ID).Scan(&task.StartedAt, &task.RetryCount); err != nil {
		return fmt.Errorf("mark processing: %w", err)
	}
	task.Status = StatusProcessing

	return nil
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

func (r *PostgresQueueRepository) MarkDoclingWaiting(ctx context.Context, id uuid.UUID, jobID string, pollCount int, nextPollAt time.Time) error {
	if pollCount < 0 {
		pollCount = 0
	}

	const query = `
UPDATE transformations_queue
SET status = 'waiting_docling', docling_job_id = $2, docling_poll_count = $3, next_poll_at = $4, started_at = NULL, error = NULL
WHERE id = $1`

	if _, err := r.pool.Exec(ctx, query, id, jobID, pollCount, nextPollAt); err != nil {
		return fmt.Errorf("mark docling waiting: %w", err)
	}

	return nil
}

func (r *PostgresQueueRepository) ActivateWaiting(ctx context.Context, now time.Time, maxDoclingLifetime time.Duration) (int, int, error) {
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return 0, 0, fmt.Errorf("begin activate tx: %w", err)
	}
	defer tx.Rollback(ctx)

	expired, err := r.failExpiredWaiting(ctx, tx, now, maxDoclingLifetime)
	if err != nil {
		return 0, 0, err
	}

	ready, err := r.releaseWaiting(ctx, tx, now)
	if err != nil {
		return 0, 0, err
	}

	if err := tx.Commit(ctx); err != nil {
		return 0, 0, fmt.Errorf("commit activate waiting: %w", err)
	}

	return ready, expired, nil
}

func (r *PostgresQueueRepository) failExpiredWaiting(ctx context.Context, tx pgx.Tx, now time.Time, maxDoclingLifetime time.Duration) (int, error) {
	if maxDoclingLifetime <= 0 {
		return 0, nil
	}

	const query = `UPDATE transformations_queue SET status = 'failed', finished_at = NOW(), error = 'docling job lifetime exceeded' WHERE status = 'waiting_docling' AND created_at < $1 RETURNING 1`

	rows, err := tx.Query(ctx, query, now.Add(-maxDoclingLifetime))
	if err != nil {
		return 0, fmt.Errorf("expire waiting: %w", err)
	}
	defer rows.Close()

	return countRows(rows)
}

func (r *PostgresQueueRepository) releaseWaiting(ctx context.Context, tx pgx.Tx, now time.Time) (int, error) {
	const query = `UPDATE transformations_queue SET status = 'pending', started_at = NULL, next_poll_at = NULL, error = NULL WHERE status = 'waiting_docling' AND (next_poll_at IS NULL OR next_poll_at <= $1) RETURNING 1`

	rows, err := tx.Query(ctx, query, now)
	if err != nil {
		return 0, fmt.Errorf("release waiting: %w", err)
	}
	defer rows.Close()

	ready, err := countRows(rows)
	if err != nil {
		return 0, err
	}

	return ready, nil
}

func (r *PostgresQueueRepository) RestoreProcessing(ctx context.Context) (int, error) {
	const query = `
UPDATE transformations_queue
SET status = 'pending', started_at = NULL, next_poll_at = NULL, error = NULL
WHERE status = 'processing'
RETURNING 1`

	rows, err := r.pool.Query(ctx, query)
	if err != nil {
		return 0, fmt.Errorf("restore processing tasks: %w", err)
	}
	defer rows.Close()

	restored, err := countRows(rows)
	if err != nil {
		return 0, err
	}

	return restored, nil
}

func (r *PostgresQueueRepository) RestoreStuckProcessing(ctx context.Context, timeout time.Duration, maxRetries, maxDoclingPollAttempts int) (restored, failed int, err error) {
	const query = `
WITH stalled AS (
    SELECT id, retry_count, docling_job_id, docling_poll_count, started_at FROM transformations_queue
    WHERE status = 'processing' AND started_at < NOW() - $1::interval
),
failed AS (
    UPDATE transformations_queue
    SET status = 'failed', finished_at = NOW(),
        error = CASE
            WHEN docling_job_id IS NOT NULL AND docling_job_id <> '' AND $3 > 0 AND docling_poll_count >= $3 THEN
                format('docling job %s exceeded polling limit (%s/%s)', docling_job_id, docling_poll_count, $3)
            ELSE
                format('processing timeout exceeded after %s (started_at=%s, retries=%s/%s)', $1, COALESCE(started_at::text, 'unknown'), retry_count, $2)
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
    SET status = 'pending', started_at = NULL, error = format('restored after timeout %s (started_at=%s)', $1, COALESCE(started_at::text, 'unknown')),
        docling_job_id = NULL, docling_poll_count = 0, next_poll_at = NULL
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

func (r *PostgresQueueRepository) RetryFailed(ctx context.Context, olderThan time.Time, maxRetries int) (retried, exhausted int, err error) {
	if maxRetries <= 0 {
		return 0, 0, nil
	}

	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return 0, 0, fmt.Errorf("begin retry tx: %w", err)
	}
	defer tx.Rollback(ctx)

	const query = `
WITH failed AS (
    SELECT id, retry_count, error FROM transformations_queue
    WHERE status = 'failed' AND finished_at IS NOT NULL AND finished_at <= $1
),
exhausted AS (
    UPDATE transformations_queue
    SET error = format(
            'retry limit exceeded (%s/%s)%s',
            retry_count,
            $2,
            CASE WHEN error IS NOT NULL AND error <> '' THEN format(': %s', error) ELSE '' END
        ),
        finished_at = NOW()
    WHERE id IN (SELECT id FROM failed WHERE retry_count >= $2)
    RETURNING 1
),
retried AS (
    UPDATE transformations_queue
    SET status = 'pending',
        started_at = NULL,
        finished_at = NULL,
        error = NULL,
        docling_job_id = NULL,
        docling_poll_count = 0,
        next_poll_at = NULL
    WHERE id IN (SELECT id FROM failed WHERE retry_count < $2)
    RETURNING 1
)
SELECT (SELECT COUNT(*) FROM retried), (SELECT COUNT(*) FROM exhausted);`

	if err := tx.QueryRow(ctx, query, olderThan, maxRetries).Scan(&retried, &exhausted); err != nil {
		return 0, 0, fmt.Errorf("retry failed tasks: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return 0, 0, fmt.Errorf("commit retry tx: %w", err)
	}

	return retried, exhausted, nil
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

func countRows(rows pgx.Rows) (int, error) {
	count := 0
	for rows.Next() {
		count++
	}

	if err := rows.Err(); err != nil {
		return 0, err
	}

	return count, nil
}

func scanTask(row pgx.Row) (*TransformationTask, error) {
	var (
		dto             repo.TransformationDTO
		doclingJobID    sql.NullString
		sourceBucket    sql.NullString
		sourceObjectKey sql.NullString
		sourceURL       sql.NullString
	)

	if err := row.Scan(
		&dto.ID,
		&dto.Status,
		&dto.RetryCount,
		&doclingJobID,
		&dto.DoclingPollCount,
		&dto.NextPollAt,
		&dto.DocumentID,
		&dto.OriginalBucket,
		&dto.OriginalObjectKey,
		&sourceBucket,
		&sourceObjectKey,
		&sourceURL,
		&dto.MarkdownBucket,
		&dto.MarkdownObjectKey,
		&dto.FilenameExtension,
		&dto.CreatedAt,
		&dto.StartedAt,
		&dto.FinishedAt,
		&dto.Error,
		&dto.CorrelationID,
	); err != nil {
		if err == pgx.ErrNoRows {
			return nil, pgx.ErrNoRows
		}

		return nil, fmt.Errorf("scan task: %w", err)
	}

	dto.DoclingJobID = doclingJobID.String
	dto.SourceBucket = sourceBucket.String
	dto.SourceObjectKey = sourceObjectKey.String
	dto.SourceURL = sourceURL.String

	return dto.ToDomain(), nil
}
