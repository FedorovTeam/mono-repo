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

	details, err := json.Marshal(job.ErrorDetails)
	if err != nil {
		return fmt.Errorf("marshal error details: %w", err)
	}

	if job.UpdatedAt.IsZero() {
		job.UpdatedAt = time.Now().UTC()
	}

	_, err = r.db.Exec(ctx, `
        INSERT INTO preprocess_queue (
            job_id, document_id, source_bucket, source_key, source_etag, target_bucket, target_key,
            callback_url, correlation_id, target_media_type, status, error_code, error_message, error_details, updated_at
        ) VALUES (
            $1, $2, $3, $4, $5, $6, $7,
            $8, $9, $10, $11, $12, $13, COALESCE($14::jsonb, '{}'::jsonb), $15
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
		nil,
		job.Status.ToDB(),
		nullableString(job.ErrorCode),
		nullableString(job.ErrorMessage),
		string(details),
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
               callback_url, correlation_id, status, error_code, error_message, error_details, updated_at
        FROM preprocess_queue
        WHERE source_bucket = $1 AND source_key = $2 AND source_etag = $3
        LIMIT 1
    `, bucket, key, etag)
}

func (r *JobRepository) Get(ctx context.Context, id string) (domain.PreprocessJob, bool, error) {
	return r.fetchJob(ctx, `
        SELECT job_id, document_id, source_bucket, source_key, source_etag, target_bucket, target_key,
               callback_url, correlation_id, status, error_code, error_message, error_details, updated_at
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
		errorCode    *string
		errorMessage *string
		errorDetails []byte
		updatedAt    time.Time
		status       string
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
		&status,
		&errorCode,
		&errorMessage,
		&errorDetails,
		&updatedAt,
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
	job.ErrorCode = derefString(errorCode)
	job.ErrorMessage = derefString(errorMessage)
	job.UpdatedAt = updatedAt
	job.Status = domain.StatusFromDB(status)

	if len(errorDetails) > 0 {
		details := make(map[string]string)
		if err := json.Unmarshal(errorDetails, &details); err == nil {
			job.ErrorDetails = details
		}
	}
	if job.ErrorDetails == nil {
		job.ErrorDetails = map[string]string{}
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
