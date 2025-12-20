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

func (r *QueueRepository) NextJobForProcessing(ctx context.Context) (domain.PreprocessJob, bool, error) {
	tx, err := r.db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return domain.PreprocessJob{}, false, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx)

	row := tx.QueryRow(ctx, `
        SELECT job_id, document_id, source_bucket, source_key, source_etag, target_bucket, target_key, target_media_type,
               callback_url, correlation_id, status, attempts, max_attempts, next_run_at, started_at, finished_at,
               error_code, error_message, error_details, created_at, updated_at
        FROM preprocess_queue
        WHERE status IN ($1, $2) AND next_run_at <= now()
        ORDER BY next_run_at
        LIMIT 1
        FOR UPDATE SKIP LOCKED
    `, toStatusString(domain.StatusPending), toStatusString(domain.StatusFailed))

	job, err := scanJob(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.PreprocessJob{}, false, tx.Commit(ctx)
	}
	if err != nil {
		return domain.PreprocessJob{}, false, fmt.Errorf("select job: %w", err)
	}

	job, err = job.MarkProcessing(time.Now().UTC())
	if err != nil {
		return domain.PreprocessJob{}, false, err
	}

	if _, err := tx.Exec(ctx, `
        UPDATE preprocess_queue
        SET status = $1, attempts = $2, started_at = $3, updated_at = $3
        WHERE job_id = $4
    `, toStatusString(job.Status), job.Attempts, job.StartedAt, job.ID); err != nil {
		return domain.PreprocessJob{}, false, fmt.Errorf("mark processing: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return domain.PreprocessJob{}, false, fmt.Errorf("commit tx: %w", err)
	}

	return job, true, nil
}

func (r *QueueRepository) MarkDone(ctx context.Context, job domain.PreprocessJob) error {
	var err error
	job, err = job.MarkCompleted(time.Now().UTC())
	if err != nil {
		return err
	}

	_, err = r.db.Exec(ctx, `
        UPDATE preprocess_queue
        SET status = $2, finished_at = $3, updated_at = $3, target_bucket = $4, target_key = $5,
            target_media_type = $6, error_code = NULL, error_message = NULL, error_details = '{}'::jsonb
        WHERE job_id = $1
    `, job.ID, toStatusString(job.Status), job.FinishedAt, job.TargetBucket, job.TargetKey, job.TargetMediaType)
	return err
}

func (r *QueueRepository) MarkFailed(ctx context.Context, job domain.PreprocessJob, update ports.FailureUpdate) error {
	finishedAt := job.UpdatedAt
	if finishedAt.IsZero() {
		finishedAt = time.Now().UTC()
	}

	if update.Details == nil {
		update.Details = map[string]string{}
	}

	detailsJSON, err := json.Marshal(update.Details)
	if err != nil {
		return fmt.Errorf("marshal error details: %w", err)
	}

	if update.TargetBucket != "" {
		job.TargetBucket = update.TargetBucket
	}
	if update.TargetKey != "" {
		job.TargetKey = update.TargetKey
	}

	job.UpdatedAt = finishedAt
	if update.Status == domain.StatusStopped {
		job.FinishedAt = finishedAt
	}

	if err := job.Validate(); err != nil {
		return fmt.Errorf("validate job %s: %w", job.ID, err)
	}

	_, err = r.db.Exec(ctx, `
        UPDATE preprocess_queue
        SET status = $2, next_run_at = $3, finished_at = CASE WHEN $2 = $5 THEN $4 ELSE finished_at END,
            error_code = $6, error_message = $7, error_details = COALESCE($8::jsonb, '{}'::jsonb),
            target_bucket = COALESCE($9, target_bucket), target_key = COALESCE($10, target_key), updated_at = $4
        WHERE job_id = $1
    `, job.ID, toStatusString(update.Status), update.NextRunAt, finishedAt, toStatusString(domain.StatusStopped), update.Code.String(), update.Message, string(detailsJSON), nullableString(update.TargetBucket), nullableString(update.TargetKey))

	return err
}

func scanJob(row pgx.Row) (domain.PreprocessJob, error) {
	var (
		job          domain.PreprocessJob
		targetBucket *string
		targetKey    *string
		targetMedia  *string
		callbackURL  *string
		correlation  *string
		errorCode    *string
		errorMessage *string
		errorDetails []byte
		createdAt    time.Time
		updatedAt    time.Time
		status       string
		nextRunAt    time.Time
		attempts     int
		maxAttempts  int
		startedAt    *time.Time
		finishedAt   *time.Time
	)

	if err := row.Scan(
		&job.ID,
		&job.DocumentID,
		&job.SourceBucket,
		&job.SourceKey,
		&job.SourceETag,
		&targetBucket,
		&targetKey,
		&targetMedia,
		&callbackURL,
		&correlation,
		&status,
		&attempts,
		&maxAttempts,
		&nextRunAt,
		&startedAt,
		&finishedAt,
		&errorCode,
		&errorMessage,
		&errorDetails,
		&createdAt,
		&updatedAt,
	); err != nil {
		return domain.PreprocessJob{}, err
	}

	job.TargetBucket = derefString(targetBucket)
	job.TargetKey = derefString(targetKey)
	job.TargetMediaType = derefString(targetMedia)
	job.CallbackURL = derefString(callbackURL)
	job.CorrelationID = derefString(correlation)
	job.Status = statusFromString(status)
	job.Attempts = attempts
	job.MaxAttempts = maxAttempts
	job.NextRunAt = nextRunAt
	job.StartedAt = derefTime(startedAt)
	job.FinishedAt = derefTime(finishedAt)
	job.ErrorCode = derefString(errorCode)
	job.ErrorMessage = derefString(errorMessage)
	job.CreatedAt = createdAt
	job.UpdatedAt = updatedAt

	if len(errorDetails) > 0 {
		details := make(map[string]string)
		if err := json.Unmarshal(errorDetails, &details); err == nil {
			job.ErrorDetails = details
		}
	}
	if job.ErrorDetails == nil {
		job.ErrorDetails = map[string]string{}
	}

	return job, nil
}
