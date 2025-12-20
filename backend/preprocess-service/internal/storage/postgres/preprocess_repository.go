package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"preprocess-service/internal/domain"
)

type pgxPool interface {
	BeginTx(ctx context.Context, txOptions pgx.TxOptions) (pgx.Tx, error)
	Exec(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

type PreprocessRepository struct {
	db pgxPool
}

func NewPreprocessRepository(db pgxPool) *PreprocessRepository {
	return &PreprocessRepository{db: db}
}

func (r *PreprocessRepository) Create(ctx context.Context, job domain.PreprocessJob) error {
	normalized := normalizeJob(job)

	detailsJSON, err := json.Marshal(normalized.ErrorDetails)
	if err != nil {
		return fmt.Errorf("marshal error details: %w", err)
	}

	if err := normalized.Validate(); err != nil {
		return fmt.Errorf("validate job: %w", err)
	}

	_, err = r.db.Exec(ctx, `
        INSERT INTO preprocess_queue (
            job_id, document_id, source_bucket, source_key, source_etag, target_bucket, target_key,
            callback_url, correlation_id, target_media_type, status, attempts, max_attempts, next_run_at,
            started_at, finished_at, error_code, error_message, error_details, created_at, updated_at
        ) VALUES (
            $1, $2, $3, $4, $5, $6, $7,
            $8, $9, $10, $11, $12, $13, $14,
            $15, $16, $17, $18, COALESCE($19::jsonb, '{}'::jsonb), $20, $21
        )
    `,
		normalized.ID,
		normalized.DocumentID,
		normalized.SourceBucket,
		normalized.SourceKey,
		normalized.SourceETag,
		nullableString(normalized.TargetBucket),
		nullableString(normalized.TargetKey),
		nullableString(normalized.CallbackURL),
		nullableString(normalized.CorrelationID),
		nullableString(normalized.TargetMediaType),
		toStatusString(normalized.Status),
		normalized.Attempts,
		normalized.MaxAttempts,
		normalized.NextRunAt,
		nullableTime(normalized.StartedAt),
		nullableTime(normalized.FinishedAt),
		nullableString(normalized.ErrorCode),
		nullableString(normalized.ErrorMessage),
		string(detailsJSON),
		normalized.CreatedAt,
		normalized.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("insert job: %w", err)
	}

	return nil
}

func (r *PreprocessRepository) FindBySource(ctx context.Context, bucket, key, etag string) (domain.PreprocessJob, bool, error) {
	return r.fetchJob(ctx, `
        SELECT `+jobColumns+`
        FROM preprocess_queue
        WHERE source_bucket = $1 AND source_key = $2 AND source_etag = $3
        LIMIT 1
    `, bucket, key, etag)
}

func (r *PreprocessRepository) Get(ctx context.Context, id string) (domain.PreprocessJob, bool, error) {
	return r.fetchJob(ctx, `
        SELECT `+jobColumns+`
        FROM preprocess_queue
        WHERE job_id = $1
        LIMIT 1
    `, id)
}

func (r *PreprocessRepository) NextForProcessing(ctx context.Context) (domain.PreprocessJob, bool, error) {
	tx, err := r.db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return domain.PreprocessJob{}, false, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx)

	row := tx.QueryRow(ctx, `
        SELECT `+jobColumns+`
        FROM preprocess_queue
        WHERE status IN ($1, $2) AND next_run_at <= now()
        ORDER BY next_run_at
        LIMIT 1
        FOR UPDATE SKIP LOCKED
    `, toStatusString(domain.StatusPending), toStatusString(domain.StatusFailed))

	job, err := scanJob(row)
	if errors.Is(err, pgx.ErrNoRows) {
		if commitErr := tx.Commit(ctx); commitErr != nil {
			return domain.PreprocessJob{}, false, fmt.Errorf("commit empty tx: %w", commitErr)
		}
		return domain.PreprocessJob{}, false, nil
	}
	if err != nil {
		return domain.PreprocessJob{}, false, fmt.Errorf("select job: %w", err)
	}

	updatedJob, err := job.MarkProcessing(time.Now().UTC())
	if err != nil {
		return domain.PreprocessJob{}, false, err
	}

	if _, err := tx.Exec(ctx, `
        UPDATE preprocess_queue
        SET status = $1, attempts = $2, started_at = $3, updated_at = $3
        WHERE job_id = $4
    `, toStatusString(updatedJob.Status), updatedJob.Attempts, updatedJob.StartedAt, updatedJob.ID); err != nil {
		return domain.PreprocessJob{}, false, fmt.Errorf("mark processing: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return domain.PreprocessJob{}, false, fmt.Errorf("commit tx: %w", err)
	}

	return updatedJob, true, nil
}

func (r *PreprocessRepository) MarkDone(ctx context.Context, job domain.PreprocessJob) error {
	completed, err := job.MarkCompleted(time.Now().UTC())
	if err != nil {
		return err
	}

	_, err = r.db.Exec(ctx, `
        UPDATE preprocess_queue
        SET status = $2, finished_at = $3, updated_at = $3, target_bucket = $4, target_key = $5,
            target_media_type = $6, error_code = NULL, error_message = NULL, error_details = '{}'::jsonb
        WHERE job_id = $1
    `, completed.ID, toStatusString(completed.Status), completed.FinishedAt, completed.TargetBucket, completed.TargetKey, completed.TargetMediaType)
	return err
}

func (r *PreprocessRepository) MarkFailed(ctx context.Context, job domain.PreprocessJob, update domain.FailureUpdate) error {
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

func (r *PreprocessRepository) fetchJob(ctx context.Context, query string, args ...any) (domain.PreprocessJob, bool, error) {
	job, err := scanJob(r.db.QueryRow(ctx, query, args...))
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.PreprocessJob{}, false, nil
	}
	if err != nil {
		return domain.PreprocessJob{}, false, fmt.Errorf("query job: %w", err)
	}

	if err := job.Validate(); err != nil {
		return domain.PreprocessJob{}, false, fmt.Errorf("validate job %s: %w", job.ID, err)
	}

	return job, true, nil
}

const jobColumns = `
    job_id, document_id, source_bucket, source_key, source_etag, target_bucket, target_key,
    callback_url, correlation_id, target_media_type, status, attempts, max_attempts, next_run_at,
    started_at, finished_at, error_code, error_message, error_details, created_at, updated_at
`

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
		&callbackURL,
		&correlation,
		&targetMedia,
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

func normalizeJob(job domain.PreprocessJob) domain.PreprocessJob {
	if job.ErrorDetails == nil {
		job.ErrorDetails = map[string]string{}
	}

	now := time.Now().UTC()

	if job.MaxAttempts <= 0 {
		job.MaxAttempts = domain.DefaultMaxAttempts
	}
	if job.NextRunAt.IsZero() {
		job.NextRunAt = now
	}
	if job.CreatedAt.IsZero() {
		job.CreatedAt = now
	}
	if job.UpdatedAt.IsZero() {
		job.UpdatedAt = job.CreatedAt
	}

	return job
}

func nullableString(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}

func derefString(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func nullableTime(value time.Time) *time.Time {
	if value.IsZero() {
		return nil
	}
	return &value
}

func derefTime(value *time.Time) time.Time {
	if value == nil {
		return time.Time{}
	}
	return *value
}
