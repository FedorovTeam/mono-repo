package persistence

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"backend-storage-service/internal/application"
	"backend-storage-service/internal/domain"
)

// ProcessingJobRepository реализует application.ProcessingJobRepository через pgx.
type ProcessingJobRepository struct{}

// NewProcessingJobRepository создаёт репозиторий задач.
func NewProcessingJobRepository() *ProcessingJobRepository {
	return &ProcessingJobRepository{}
}

func (r *ProcessingJobRepository) SelectPendingForStage(ctx context.Context, tx application.Tx, stage string) (*domain.ProcessingJob, error) {
	row := tx.QueryRow(ctx, `
        SELECT task_id, document_id, stage, status, attempts, next_run_at, payload
        FROM processing_jobs
        WHERE stage=$1 AND status='pending' AND next_run_at <= now()
        ORDER BY next_run_at
        LIMIT 1
        FOR UPDATE SKIP LOCKED
    `, stage)

	var payloadRaw []byte
	job := &domain.ProcessingJob{}
	if err := row.Scan(&job.TaskID, &job.DocumentID, &job.Stage, &job.Status, &job.Attempts, &job.NextRunAt, &payloadRaw); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, application.ErrNoJobs
		}
		return nil, err
	}

	_ = json.Unmarshal(payloadRaw, &job.Payload)
	return job, nil
}

func (r *ProcessingJobRepository) MarkInProgress(ctx context.Context, tx application.Tx, jobID, stage, message string) error {
	if _, err := tx.Exec(ctx, `UPDATE processing_jobs SET status='in_progress', updated_at=now() WHERE task_id=$1`, jobID); err != nil {
		return err
	}

	return r.insertEvent(ctx, tx, jobID, stage, "in_progress", "processing", message)
}

func (r *ProcessingJobRepository) MarkCompleted(ctx context.Context, tx application.Tx, jobID, stage, message string) error {
	if _, err := tx.Exec(ctx, `UPDATE processing_jobs SET status='completed', updated_at=now() WHERE task_id=$1`, jobID); err != nil {
		return err
	}

	return r.insertEvent(ctx, tx, jobID, stage, "completed", "completed", message)
}

func (r *ProcessingJobRepository) RescheduleOrFail(ctx context.Context, tx application.Tx, job *domain.ProcessingJob, stage string, reason error, maxAttempts int, backoff time.Duration) error {
	attempts := job.Attempts + 1
	status := "pending"
	if attempts >= maxAttempts {
		status = "failed"
	}

	nextRun := time.Now().Add(time.Duration(attempts) * backoff)
	payload := job.Payload
	if payload == nil {
		payload = map[string]any{}
	}
	payload["last_error"] = reason.Error()

	payloadBytes, _ := json.Marshal(payload)
	if _, err := tx.Exec(ctx, `
        UPDATE processing_jobs
        SET status=$1, attempts=$2, next_run_at=$3, payload=$4, updated_at=now()
        WHERE task_id=$5
    `, status, attempts, nextRun, payloadBytes, job.TaskID); err != nil {
		return err
	}

	return r.insertEvent(ctx, tx, job.TaskID, stage, status, "retry", reason.Error())
}

func (r *ProcessingJobRepository) insertEvent(ctx context.Context, tx application.Tx, jobID, stage, status, eventType, message string) error {
	_, err := tx.Exec(ctx, `
        INSERT INTO job_events (id, job_id, stage, status, event_type, message, payload, created_at)
        VALUES (gen_random_uuid(), $1, $2, $3, $4, $5, '{}'::jsonb, now())
    `, jobID, stage, status, eventType, message)
	return err
}
