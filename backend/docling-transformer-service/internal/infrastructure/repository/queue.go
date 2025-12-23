package repository

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/mono-repo/docling-transformer-service/internal/domain"
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

const doclingSubmitLockKey int64 = 9955663377

var _ repo.QueueRepository = (*PostgresQueueRepository)(nil)

type PostgresQueueRepository struct {
	pool *pgxpool.Pool
}

type submitLock struct {
	conn     *pgxpool.Conn
	released bool
}

func (l *submitLock) Release(ctx context.Context) error {
	if l == nil || l.conn == nil || l.released {
		return nil
	}

	l.released = true

	var unlocked bool
	err := l.conn.QueryRow(ctx, "SELECT pg_advisory_unlock($1)", doclingSubmitLockKey).Scan(&unlocked)
	l.conn.Release()
	l.conn = nil
	if err != nil {
		return fmt.Errorf("release submit lock: %w", err)
	}
	if !unlocked {
		return fmt.Errorf("release submit lock: lock not held")
	}

	return nil
}

func NewQueueRepository(pool *pgxpool.Pool) *PostgresQueueRepository {
	return &PostgresQueueRepository{pool: pool}
}

var baseSelectFields = strings.Join([]string{
	"id",
	"status",
	"retry_count",
	"COALESCE(docling_task_id, '') AS docling_task_id",
	"COALESCE(docling_job_id, '') AS docling_job_id",
	"docling_poll_count",
	"next_poll_at",
	"document_id",
	"original_bucket",
	"original_object_key",
	"COALESCE(source_bucket, '') AS source_bucket",
	"COALESCE(source_object_key, '') AS source_object_key",
	"COALESCE(source_url, '') AS source_url",
	"markdown_bucket",
	"markdown_object_key",
	"filename_extension",
	"created_at",
	"started_at",
	"finished_at",
	"error",
	"correlation_id",
	"table_metadata",
	"table_count",
	"table_pages",
}, ",\n")

func (r *PostgresQueueRepository) GetLatestByDocument(ctx context.Context, documentID string) (*TransformationTask, error) {
	query := fmt.Sprintf(`
SELECT %s
FROM transformations_queue
WHERE document_id = $1
ORDER BY created_at DESC
LIMIT 1`, baseSelectFields)

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

func (r *PostgresQueueRepository) GetByID(ctx context.Context, id uuid.UUID) (*TransformationTask, error) {
	query := fmt.Sprintf(`
SELECT %s
FROM transformations_queue
WHERE id = $1`, baseSelectFields)

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
 id, status, document_id, original_bucket, original_object_key,
 source_bucket, source_object_key, source_url,
 markdown_bucket, markdown_object_key, filename_extension,
 created_at, correlation_id, docling_poll_count, next_poll_at
) VALUES (
 $1, $2, $3, $4, $5,
 NULLIF($6, ''), NULLIF($7, ''), NULLIF($8, ''),
 $9, $10, $11,
 NOW(), NULLIF($12, ''), 0, NULL
)`

	_, err := r.pool.Exec(ctx, query,
		id, StatusPending,
		params.DocumentID, params.OriginalBucket, params.OriginalObjectKey,
		params.SourceBucket, params.SourceObjectKey, params.SourceURL,
		params.MarkdownBucket, params.MarkdownObjectKey, normalizedExt,
		params.CorrelationID,
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

func (r *PostgresQueueRepository) AcquireSubmitLock(ctx context.Context) (repo.SubmitLock, error) {
	conn, err := r.pool.Acquire(ctx)
	if err != nil {
		return nil, fmt.Errorf("acquire submit lock connection: %w", err)
	}

	var locked bool
	if err := conn.QueryRow(ctx, "SELECT pg_try_advisory_lock($1)", doclingSubmitLockKey).Scan(&locked); err != nil {
		conn.Release()
		return nil, fmt.Errorf("acquire submit lock: %w", err)
	}
	if !locked {
		conn.Release()
		return nil, repo.ErrSubmitLockBusy
	}

	return &submitLock{conn: conn}, nil
}

// КЛЮЧЕВОЕ: строгая сериализация Docling.
// 1) сначала берём waiting_docling, у которых next_poll_at <= now()
// 2) если waiting_docling вообще есть (но ещё рано поллить) — НЕ берём pending (блокируем очередь)
// 3) pending берём только если waiting_docling вообще нет
func (r *PostgresQueueRepository) selectNextTasks(ctx context.Context, tx pgx.Tx, limit int) ([]*TransformationTask, error) {
	// 1) Сначала берём готовые к поллингу waiting_docling
	queryWaitingReady := fmt.Sprintf(`
SELECT %s
FROM transformations_queue
WHERE status = 'waiting_docling'
  AND next_poll_at IS NOT NULL
  AND next_poll_at <= NOW()
ORDER BY next_poll_at, created_at
LIMIT $1
FOR UPDATE SKIP LOCKED`, baseSelectFields)

	tasks, err := r.fetchTasks(ctx, tx, queryWaitingReady, 1)
	if err == nil && len(tasks) > 0 {
		return tasks, nil
	}
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}

	// 2) Если есть любые waiting_docling (даже не ready) — блокируем pending
	hasWaiting, err := r.hasWaitingDocling(ctx, tx)
	if err != nil {
		return nil, err
	}
	if hasWaiting {
		return nil, pgx.ErrNoRows
	}

	// 3) waiting_docling нет — можно брать pending (и только один/limit)
	queryPending := fmt.Sprintf(`
SELECT %s
FROM transformations_queue
WHERE status = 'pending'
  AND (next_poll_at IS NULL OR next_poll_at <= NOW())
ORDER BY created_at
LIMIT $1
FOR UPDATE SKIP LOCKED`, baseSelectFields)

	tasks, err = r.fetchTasks(ctx, tx, queryPending, limit)
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

func (r *PostgresQueueRepository) MarkDone(ctx context.Context, id uuid.UUID, markdownBucket, markdownObjectKey string, tableMetadata *domain.TableMetadata) error {
	const query = `
UPDATE transformations_queue
SET status = 'done', markdown_bucket = $1, markdown_object_key = $2,
    table_metadata = $3, table_count = $4, table_pages = $5,
    next_poll_at = NULL, finished_at = NOW(), error = NULL
WHERE id = $6`

	tableMetadata = normalizeTableMetadata(tableMetadata)
	var meta any
	var tableCount any
	var tablePages any
	if tableMetadata != nil {
		encoded, err := json.Marshal(tableMetadata)
		if err != nil {
			return fmt.Errorf("encode table metadata: %w", err)
		}
		meta = string(encoded)
		tableCount = tableMetadata.TablesCount
		if len(tableMetadata.Pages) > 0 {
			tablePages = append([]int32(nil), tableMetadata.Pages...)
		}
	}

	if _, err := r.pool.Exec(ctx, query, markdownBucket, markdownObjectKey, meta, tableCount, tablePages, id); err != nil {
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

func (r *PostgresQueueRepository) UpdateDoclingInfo(ctx context.Context, id uuid.UUID, taskID, jobID string, pollCount int) error {
	if pollCount < 0 {
		pollCount = 0
	}

	const query = `
UPDATE transformations_queue
SET docling_task_id = COALESCE(NULLIF($2, ''), docling_task_id),
    docling_job_id  = COALESCE(NULLIF($3, ''), docling_job_id),
    docling_poll_count = $4
WHERE id = $1`

	if _, err := r.pool.Exec(ctx, query, id, taskID, jobID, pollCount); err != nil {
		return fmt.Errorf("update docling info: %w", err)
	}
	return nil
}

func (r *PostgresQueueRepository) MarkDoclingWaiting(ctx context.Context, id uuid.UUID, taskID, jobID string, pollCount int, nextPollAt time.Time) error {
	if pollCount < 0 {
		pollCount = 0
	}

	const query = `
UPDATE transformations_queue
SET status = 'waiting_docling',
    docling_task_id = $2, docling_job_id = $3, docling_poll_count = $4,
    next_poll_at = $5, started_at = NULL, error = NULL
WHERE id = $1`

	if _, err := r.pool.Exec(ctx, query, id, taskID, jobID, pollCount, nextPollAt); err != nil {
		return fmt.Errorf("mark docling waiting: %w", err)
	}

	return nil
}

func (r *PostgresQueueRepository) RequeueDoclingWaiting(ctx context.Context, id uuid.UUID, nextPollAt *time.Time) error {
	const query = `
UPDATE transformations_queue
SET status = 'pending',
    retry_count = retry_count + 1,
    started_at = NULL,
    finished_at = NULL,
    error = NULL,
    docling_task_id = NULL,
    docling_job_id = NULL,
    docling_poll_count = 0,
    next_poll_at = $2
WHERE id = $1 AND status IN ('waiting_docling','processing')`

	if _, err := r.pool.Exec(ctx, query, id, nextPollAt); err != nil {
		return fmt.Errorf("requeue docling waiting: %w", err)
	}
	return nil
}

func (r *PostgresQueueRepository) ReschedulePending(ctx context.Context, id uuid.UUID, nextPollAt time.Time) error {
	const query = `
UPDATE transformations_queue
SET status = 'pending',
    started_at = NULL,
    finished_at = NULL,
    error = NULL,
    docling_task_id = NULL,
    docling_job_id = NULL,
    docling_poll_count = 0,
    next_poll_at = $2
WHERE id = $1`

	if _, err := r.pool.Exec(ctx, query, id, nextPollAt); err != nil {
		return fmt.Errorf("reschedule pending: %w", err)
	}

	return nil
}

// ВАЖНО: ActivateWaiting больше НЕ переводит waiting_docling->pending.
// Теперь он только “протухшие” помечает failed.
// Сериализация достигается тем, что пока есть waiting_docling — pending не выдаём.
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

	if err := tx.Commit(ctx); err != nil {
		return 0, 0, fmt.Errorf("commit activate waiting: %w", err)
	}

	// activated всегда 0 (мы ничего не “активируем”)
	return 0, expired, nil
}

func (r *PostgresQueueRepository) failExpiredWaiting(ctx context.Context, tx pgx.Tx, now time.Time, maxDoclingLifetime time.Duration) (int, error) {
	if maxDoclingLifetime <= 0 {
		return 0, nil
	}

	const query = `
UPDATE transformations_queue
SET status = 'failed', finished_at = NOW(), error = 'docling job lifetime exceeded'
WHERE status = 'waiting_docling' AND created_at < $1
RETURNING 1`

	rows, err := tx.Query(ctx, query, now.Add(-maxDoclingLifetime))
	if err != nil {
		return 0, fmt.Errorf("expire waiting: %w", err)
	}
	defer rows.Close()

	return countRows(rows)
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

	return countRows(rows)
}

func (r *PostgresQueueRepository) RestoreStuckProcessing(ctx context.Context, timeout time.Duration, maxRetries, maxDoclingPollAttempts int) (restored, failed int, err error) {
	const query = `
WITH stalled AS (
    SELECT id, retry_count, docling_job_id, docling_poll_count, started_at
    FROM transformations_queue
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
    SET status = 'pending', started_at = NULL,
        error = format('restored after timeout %s (started_at=%s)', $1, COALESCE(started_at::text, 'unknown')),
        docling_task_id = NULL, docling_job_id = NULL, docling_poll_count = 0, next_poll_at = NULL
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
        docling_task_id = NULL,
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

func normalizePages(pages []int32) []int32 {
	if len(pages) == 0 {
		return nil
	}
	seen := make(map[int32]struct{}, len(pages))
	result := make([]int32, 0, len(pages))
	for _, page := range pages {
		if _, ok := seen[page]; ok {
			continue
		}
		seen[page] = struct{}{}
		result = append(result, page)
	}
	sort.Slice(result, func(i, j int) bool { return result[i] < result[j] })
	return result
}

func normalizeTableMetadata(meta *domain.TableMetadata) *domain.TableMetadata {
	if meta == nil {
		return nil
	}
	result := &domain.TableMetadata{TablesCount: meta.TablesCount}
	result.Pages = normalizePages(meta.Pages)
	if result.TablesCount == 0 && len(result.Pages) > 0 {
		result.TablesCount = len(result.Pages)
	}
	if result.TablesCount == 0 && len(result.Pages) == 0 {
		return nil
	}
	return result
}

func scanTask(row pgx.Row) (*TransformationTask, error) {
	var (
		dto             repo.TransformationDTO
		doclingTaskID   string
		doclingJobID    string
		sourceBucket    string
		sourceObjectKey string
		sourceURL       string
		tableMetadata   []byte
		tableCount      *int32
		tablePages      []int32
	)

	if err := row.Scan(
		&dto.ID,
		&dto.Status,
		&dto.RetryCount,
		&doclingTaskID,
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
		&tableMetadata,
		&tableCount,
		&tablePages,
	); err != nil {
		if err == pgx.ErrNoRows {
			return nil, pgx.ErrNoRows
		}
		return nil, fmt.Errorf("scan task: %w", err)
	}

	dto.DoclingTaskID = doclingTaskID
	dto.DoclingJobID = doclingJobID
	dto.SourceBucket = sourceBucket
	dto.SourceObjectKey = sourceObjectKey
	dto.SourceURL = sourceURL
	dto.TablePages = normalizePages(tablePages)
	if len(tableMetadata) > 0 && string(bytes.TrimSpace(tableMetadata)) != "null" {
		if err := json.Unmarshal(tableMetadata, &dto.TableMetadata); err != nil {
			return nil, fmt.Errorf("parse table metadata: %w", err)
		}
	}
	if tableCount != nil {
		dto.TableCount = int(*tableCount)
	}
	if dto.TableMetadata != nil {
		if dto.TableMetadata.TablesCount == 0 && dto.TableCount > 0 {
			dto.TableMetadata.TablesCount = dto.TableCount
		}
		if len(dto.TableMetadata.Pages) == 0 && len(dto.TablePages) > 0 {
			dto.TableMetadata.Pages = append([]int32(nil), dto.TablePages...)
		}
	}
	if dto.TableMetadata == nil && (dto.TableCount > 0 || len(dto.TablePages) > 0) {
		dto.TableMetadata = &domain.TableMetadata{
			TablesCount: dto.TableCount,
			Pages:       append([]int32(nil), dto.TablePages...),
		}
	}
	dto.TableMetadata = normalizeTableMetadata(dto.TableMetadata)
	if dto.TableMetadata == nil {
		dto.TableCount = 0
		dto.TablePages = nil
	} else {
		dto.TableCount = dto.TableMetadata.TablesCount
		dto.TablePages = normalizePages(append(dto.TablePages, dto.TableMetadata.Pages...))
		if dto.TableCount == 0 && len(dto.TablePages) > 0 {
			dto.TableCount = len(dto.TablePages)
		}
	}

	return dto.ToDomain(), nil
}
