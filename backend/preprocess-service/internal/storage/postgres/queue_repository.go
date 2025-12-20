package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"preprocess-service/internal/domain"
	"preprocess-service/internal/usecase/ports"
)

// QueueRepository implements ports.QueueRepository backed by PostgreSQL preprocess_queue.
type QueueRepository struct {
	db *pgxpool.Pool
}

func NewQueueRepository(db *pgxpool.Pool) *QueueRepository {
	return &QueueRepository{db: db}
}

func (r *QueueRepository) NextJobForProcessing(ctx context.Context) (ports.Job, bool, error) {
	tx, err := r.db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return ports.Job{}, false, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx)

	row := tx.QueryRow(ctx, `
        SELECT job_id, source_bucket, source_key, source_etag, target_bucket, target_key, attempts, max_attempts, callback_url, correlation_id
        FROM preprocess_queue
        WHERE status IN ($1, $2) AND next_run_at <= now()
        ORDER BY next_run_at
        LIMIT 1
        FOR UPDATE SKIP LOCKED
    `, domain.StatusPending.ToDB(), domain.StatusFailed.ToDB())

	var job ports.Job
	err = row.Scan(
		&job.JobID,
		&job.SourceBucket,
		&job.SourceKey,
		&job.SourceETag,
		&job.TargetBucket,
		&job.TargetKey,
		&job.Attempts,
		&job.MaxAttempts,
		&job.CallbackURL,
		&job.CorrelationID,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return ports.Job{}, false, tx.Commit(ctx)
	}
	if err != nil {
		return ports.Job{}, false, fmt.Errorf("select job: %w", err)
	}

	job.Attempts++

	if _, err := tx.Exec(ctx, `
        UPDATE preprocess_queue
        SET status = $1, attempts = attempts + 1, started_at = now(), updated_at = now()
        WHERE job_id = $2
    `, domain.StatusProcessing.ToDB(), job.JobID); err != nil {
		return ports.Job{}, false, fmt.Errorf("mark processing: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return ports.Job{}, false, fmt.Errorf("commit tx: %w", err)
	}

	return job, true, nil
}

func (r *QueueRepository) MarkDone(ctx context.Context, job ports.Job, targetMediaType string) error {
	finishedAt := time.Now().UTC()
	_, err := r.db.Exec(ctx, `
        UPDATE preprocess_queue
        SET status = $2, finished_at = $3, updated_at = $3, target_bucket = $4, target_key = $5,
            target_media_type = $6, error_code = NULL, error_message = NULL, error_details = '{}'::jsonb
        WHERE job_id = $1
    `, job.JobID, domain.StatusCompleted.ToDB(), finishedAt, job.TargetBucket, job.TargetKey, targetMediaType)
	return err
}

func (r *QueueRepository) MarkFailed(ctx context.Context, job ports.Job, update ports.FailureUpdate) error {
	finishedAt := time.Now().UTC()

	if update.Details == nil {
		update.Details = map[string]any{}
	}

	detailsJSON, err := json.Marshal(update.Details)
	if err != nil {
		return fmt.Errorf("marshal error details: %w", err)
	}

	_, err = r.db.Exec(ctx, `
        UPDATE preprocess_queue
        SET status = $2, next_run_at = $3, finished_at = CASE WHEN $2 = $5 THEN $4 ELSE finished_at END,
            error_code = $6, error_message = $7, error_details = COALESCE($8::jsonb, '{}'::jsonb),
            target_bucket = COALESCE($9, target_bucket), target_key = COALESCE($10, target_key), updated_at = $4
        WHERE job_id = $1
    `, job.JobID, update.Status, update.NextRunAt, finishedAt, domain.StatusStopped.ToDB(), update.Code, update.Message, string(detailsJSON), nullableString(update.TargetBucket), nullableString(update.TargetKey))

	return err
}
