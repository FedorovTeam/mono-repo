package persistence

import (
	"context"
	"errors"
	"regexp"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	pgxmock "github.com/pashagolub/pgxmock/v3"

	"backend-storage-service/internal/application"
	"backend-storage-service/internal/domain"
)

func TestProcessingJobRepository_SelectPendingForStage(t *testing.T) {
	ctx := context.Background()
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer mock.Close()

	repo := NewProcessingJobRepository()

	mock.ExpectBeginTx(pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	rows := pgxmock.NewRows([]string{"task_id", "document_id", "stage", "status", "attempts", "next_run_at", "payload"}).
		AddRow("task", "doc", "store_original", "pending", 0, time.Now(), []byte(`{"correlation_id":"c1"}`))
	tx, _ := mock.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	mock.ExpectQuery(regexp.QuoteMeta("SELECT task_id, document_id, stage, status, attempts, next_run_at, payload FROM processing_jobs WHERE stage=$1 AND status='pending' AND next_run_at <= now() ORDER BY next_run_at LIMIT 1 FOR UPDATE SKIP LOCKED")).
		WithArgs("store_original").
		WillReturnRows(rows)
	mock.ExpectCommit()

	job, err := repo.SelectPendingForStage(ctx, tx, "store_original")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if job.TaskID != "task" || job.DocumentID != "doc" {
		t.Fatalf("unexpected job: %+v", job)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

func TestProcessingJobRepository_SelectPendingForStage_NoRows(t *testing.T) {
	ctx := context.Background()
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer mock.Close()

	repo := NewProcessingJobRepository()

	mock.ExpectBeginTx(pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	tx, _ := mock.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	mock.ExpectQuery(regexp.QuoteMeta("SELECT task_id, document_id, stage, status, attempts, next_run_at, payload FROM processing_jobs WHERE stage=$1 AND status='pending' AND next_run_at <= now() ORDER BY next_run_at LIMIT 1 FOR UPDATE SKIP LOCKED")).
		WithArgs("store_original").
		WillReturnError(pgx.ErrNoRows)
	mock.ExpectRollback()

	_, err = repo.SelectPendingForStage(ctx, tx, "store_original")
	if !errors.Is(err, application.ErrNoJobs) {
		t.Fatalf("expected ErrNoJobs, got %v", err)
	}
	_ = tx.Rollback(ctx)

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

func TestProcessingJobRepository_StatusUpdates(t *testing.T) {
	ctx := context.Background()
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer mock.Close()

	repo := NewProcessingJobRepository()
	mock.ExpectBeginTx(pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	tx, _ := mock.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})

	mock.ExpectExec(regexp.QuoteMeta("UPDATE processing_jobs SET status='in_progress', updated_at=now() WHERE task_id=$1")).
		WithArgs("task").
		WillReturnResult(pgxmock.NewResult("UPDATE", 1))
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO job_events (id, job_id, stage, status, event_type, message, payload, created_at) VALUES (gen_random_uuid(), $1, $2, $3, $4, $5, '{}'::jsonb, now())")).
		WithArgs("task", "stage", "in_progress", "processing", "picked job").
		WillReturnResult(pgxmock.NewResult("INSERT", 1))

	if err := repo.MarkInProgress(ctx, tx, "task", "stage", "picked job"); err != nil {
		t.Fatalf("mark in progress: %v", err)
	}

	mock.ExpectExec(regexp.QuoteMeta("UPDATE processing_jobs SET status='completed', updated_at=now() WHERE task_id=$1")).
		WithArgs("task").
		WillReturnResult(pgxmock.NewResult("UPDATE", 1))
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO job_events (id, job_id, stage, status, event_type, message, payload, created_at) VALUES (gen_random_uuid(), $1, $2, $3, $4, $5, '{}'::jsonb, now())")).
		WithArgs("task", "stage", "completed", "completed", "done").
		WillReturnResult(pgxmock.NewResult("INSERT", 1))

	if err := repo.MarkCompleted(ctx, tx, "task", "stage", "done"); err != nil {
		t.Fatalf("mark completed: %v", err)
	}

	job := &domain.ProcessingJob{TaskID: "task", Attempts: 1, Payload: map[string]any{"foo": "bar"}}
	mock.ExpectExec(regexp.QuoteMeta("UPDATE processing_jobs SET status=$1, attempts=$2, next_run_at=$3, payload=$4, updated_at=now() WHERE task_id=$5")).
		WithArgs("pending", 2, pgxmock.AnyArg(), pgxmock.AnyArg(), "task").
		WillReturnResult(pgxmock.NewResult("UPDATE", 1))
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO job_events (id, job_id, stage, status, event_type, message, payload, created_at) VALUES (gen_random_uuid(), $1, $2, $3, $4, $5, '{}'::jsonb, now())")).
		WithArgs("task", "stage", "pending", "retry", "boom").
		WillReturnResult(pgxmock.NewResult("INSERT", 1))
	mock.ExpectCommit()

	if err := repo.RescheduleOrFail(ctx, tx, job, "stage", errors.New("boom"), 3, time.Second); err != nil {
		t.Fatalf("reschedule: %v", err)
	}

	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}
