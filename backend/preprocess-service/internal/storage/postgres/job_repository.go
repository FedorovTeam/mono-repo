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
)

// JobRepository implements domain.JobRepository backed by PostgreSQL preprocess_queue.
type JobRepository struct {
	db *pgxpool.Pool
}

func NewJobRepository(db *pgxpool.Pool) *JobRepository {
	return &JobRepository{db: db}
}

func (r *JobRepository) Save(ctx context.Context, job domain.PreprocessJob) error {
	if job.ErrorDetails == nil {
		job.ErrorDetails = map[string]string{}
	}

	if job.MaxAttempts <= 0 {
		job.MaxAttempts = domain.DefaultMaxAttempts
	}

	if job.NextRunAt.IsZero() {
		job.NextRunAt = time.Now().UTC()
	}

	if job.CreatedAt.IsZero() {
		job.CreatedAt = time.Now().UTC()
	}
	if job.UpdatedAt.IsZero() {
		job.UpdatedAt = job.CreatedAt
	}

	details, err := json.Marshal(job.ErrorDetails)
	if err != nil {
		return fmt.Errorf("marshal error details: %w", err)
	}

	if err := job.Validate(); err != nil {
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
		job.ID,
		job.DocumentID,
		job.SourceBucket,
		job.SourceKey,
		job.SourceETag,
		nullableString(job.TargetBucket),
		nullableString(job.TargetKey),
		nullableString(job.CallbackURL),
		nullableString(job.CorrelationID),
		nullableString(job.TargetMediaType),
		toStatusString(job.Status),
		job.Attempts,
		job.MaxAttempts,
		job.NextRunAt,
		nullableTime(job.StartedAt),
		nullableTime(job.FinishedAt),
		nullableString(job.ErrorCode),
		nullableString(job.ErrorMessage),
		string(details),
		job.CreatedAt,
		job.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("insert job: %w", err)
	}

	return nil
}

func (r *JobRepository) FindBySource(ctx context.Context, bucket, key, etag string) (domain.PreprocessJob, bool, error) {
	return r.fetchJob(ctx, `
        SELECT job_id, document_id, source_bucket, source_key, source_etag, target_bucket, target_key,
               callback_url, correlation_id, target_media_type, status, attempts, max_attempts, next_run_at,
               started_at, finished_at, error_code, error_message, error_details, updated_at, created_at
        FROM preprocess_queue
        WHERE source_bucket = $1 AND source_key = $2 AND source_etag = $3
        LIMIT 1
    `, bucket, key, etag)
}

func (r *JobRepository) Get(ctx context.Context, id string) (domain.PreprocessJob, bool, error) {
	return r.fetchJob(ctx, `
        SELECT job_id, document_id, source_bucket, source_key, source_etag, target_bucket, target_key,
               callback_url, correlation_id, target_media_type, status, attempts, max_attempts, next_run_at,
               started_at, finished_at, error_code, error_message, error_details, updated_at, created_at
        FROM preprocess_queue
        WHERE job_id = $1
        LIMIT 1
    `, id)
}

func (r *JobRepository) fetchJob(ctx context.Context, query string, args ...any) (domain.PreprocessJob, bool, error) {
	var (
		job          domain.PreprocessJob
		targetBucket *string
		targetKey    *string
		callbackURL  *string
		correlation  *string
		targetMedia  *string
		errorCode    *string
		errorMessage *string
		errorDetails []byte
		updatedAt    time.Time
		createdAt    time.Time
		status       string
		nextRunAt    time.Time
		attempts     int
		maxAttempts  int
		startedAt    *time.Time
		finishedAt   *time.Time
	)

	err := r.db.QueryRow(ctx, query, args...).Scan(
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
		&updatedAt,
		&createdAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.PreprocessJob{}, false, nil
	}
	if err != nil {
		return domain.PreprocessJob{}, false, fmt.Errorf("query job: %w", err)
	}

	job.TargetBucket = derefString(targetBucket)
	job.TargetKey = derefString(targetKey)
	job.CallbackURL = derefString(callbackURL)
	job.CorrelationID = derefString(correlation)
	job.TargetMediaType = derefString(targetMedia)
	job.ErrorCode = derefString(errorCode)
	job.ErrorMessage = derefString(errorMessage)
	job.Status = statusFromString(status)
	job.Attempts = attempts
	job.MaxAttempts = maxAttempts
	job.NextRunAt = nextRunAt
	job.StartedAt = derefTime(startedAt)
	job.FinishedAt = derefTime(finishedAt)
	job.UpdatedAt = updatedAt
	job.CreatedAt = createdAt

	if len(errorDetails) > 0 {
		details := make(map[string]string)
		if err := json.Unmarshal(errorDetails, &details); err == nil {
			job.ErrorDetails = details
		}
	}
	if job.ErrorDetails == nil {
		job.ErrorDetails = map[string]string{}
	}

	if err := job.Validate(); err != nil {
		return domain.PreprocessJob{}, false, fmt.Errorf("validate job %s: %w", job.ID, err)
	}

	return job, true, nil
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
