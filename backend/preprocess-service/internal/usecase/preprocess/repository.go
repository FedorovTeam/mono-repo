package preprocess

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type QueueRepository interface {
	MarkDone(ctx context.Context, job Job, targetMediaType string) error
	MarkFailed(ctx context.Context, job Job, update FailureUpdate) error
}

type PGQueueRepository struct {
	db *pgxpool.Pool
}

func NewPGQueueRepository(db *pgxpool.Pool) *PGQueueRepository {
	return &PGQueueRepository{db: db}
}

func (r *PGQueueRepository) MarkDone(ctx context.Context, job Job, targetMediaType string) error {
	finishedAt := time.Now().UTC()
	_, err := r.db.Exec(ctx, `
        UPDATE preprocess_queue
        SET status = $2, finished_at = $3, updated_at = $3, target_bucket = $4, target_key = $5,
            target_media_type = $6, error_code = NULL, error_message = NULL, error_details = '{}'::jsonb
        WHERE job_id = $1
    `, job.JobID, StatusDone, finishedAt, job.TargetBucket, job.TargetKey, targetMediaType)
	return err
}

func (r *PGQueueRepository) MarkFailed(ctx context.Context, job Job, update FailureUpdate) error {
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
    `, job.JobID, update.Status, update.NextRunAt, finishedAt, StatusStopped, update.Code, update.Message, string(detailsJSON), nullableString(update.TargetBucket), nullableString(update.TargetKey))

	return err
}

func nullableString(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}
