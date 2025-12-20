package postgres

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/pashagolub/pgxmock/v3"

	"preprocess-service/internal/domain"
)

func TestPreprocessRepository_NextForProcessingLocksAndMarksProcessing(t *testing.T) {
	ctx := context.Background()
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatalf("create mock pool: %v", err)
	}
	defer mock.Close()

	repo := NewPreprocessRepository(mock)

	now := time.Now().UTC()
	job := domain.PreprocessJob{
		ID:           "job-next",
		DocumentID:   "doc-1",
		SourceBucket: "source",
		SourceKey:    "input.txt",
		SourceETag:   "etag-1",
		Status:       domain.StatusPending,
		Attempts:     0,
		MaxAttempts:  3,
		NextRunAt:    now.Add(-time.Minute),
		CreatedAt:    now.Add(-2 * time.Minute),
		UpdatedAt:    now.Add(-time.Minute),
	}

	rows := pgxmock.NewRows(jobColumnNames()).
		AddRow(job.ID, job.DocumentID, job.SourceBucket, job.SourceKey, job.SourceETag, nil, nil, nil, nil, nil,
			toStatusString(job.Status), job.Attempts, job.MaxAttempts, job.NextRunAt, nil, nil, nil, nil, []byte("{}"), job.CreatedAt, job.UpdatedAt)

	mock.ExpectBeginTx(pgx.TxOptions{})
	mock.ExpectQuery("SELECT .*FOR UPDATE SKIP LOCKED").
		WithArgs(toStatusString(domain.StatusPending), toStatusString(domain.StatusFailed)).
		WillReturnRows(rows)
	mock.ExpectExec("UPDATE preprocess_queue").
		WithArgs(
			toStatusString(domain.StatusProcessing),
			job.Attempts+1,
			pgxmock.AnyArg(),
			job.ID,
		).
		WillReturnResult(pgxmock.NewResult("UPDATE", 1))
	mock.ExpectCommit()

	picked, found, err := repo.NextForProcessing(ctx)
	if err != nil {
		t.Fatalf("NextForProcessing returned error: %v", err)
	}
	if !found {
		t.Fatal("expected job to be found")
	}

	if picked.Status != domain.StatusProcessing {
		t.Fatalf("expected status processing, got %s", picked.Status)
	}
	if picked.Attempts != job.Attempts+1 {
		t.Fatalf("expected attempts incremented to %d, got %d", job.Attempts+1, picked.Attempts)
	}
	if picked.StartedAt.IsZero() {
		t.Fatal("expected started_at to be set")
	}
	if picked.UpdatedAt.IsZero() {
		t.Fatal("expected updated_at to be set")
	}
	if !picked.NextRunAt.Equal(job.NextRunAt) {
		t.Fatalf("expected next_run_at to stay %v, got %v", job.NextRunAt, picked.NextRunAt)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

func TestPreprocessRepository_MarkFailedUpdatesStatus(t *testing.T) {
	ctx := context.Background()
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatalf("create mock pool: %v", err)
	}
	defer mock.Close()

	repo := NewPreprocessRepository(mock)

	now := time.Now().UTC()
	job := domain.PreprocessJob{
		ID:           "job-failed",
		DocumentID:   "doc-2",
		SourceBucket: "source",
		SourceKey:    "input.txt",
		SourceETag:   "etag-2",
		Status:       domain.StatusProcessing,
		Attempts:     2,
		MaxAttempts:  3,
		NextRunAt:    now.Add(-time.Minute),
		CreatedAt:    now.Add(-2 * time.Minute),
		UpdatedAt:    now,
		TargetBucket: "target",
		TargetKey:    "converted/out.docx",
	}

	update := domain.FailureUpdate{
		Status:       domain.StatusStopped,
		NextRunAt:    now.Add(time.Hour),
		Code:         domain.ErrorCodeConvertFailed,
		Message:      "conversion failed",
		TargetBucket: "target",
		TargetKey:    "converted/out.docx",
		Details:      map[string]string{"reason": "timeout"},
	}

	mock.ExpectExec("UPDATE preprocess_queue").
		WithArgs(
			job.ID,
			toStatusString(update.Status),
			update.NextRunAt,
			job.UpdatedAt,
			toStatusString(domain.StatusStopped),
			update.Code.String(),
			update.Message,
			`{"reason":"timeout"}`,
			pgxmock.AnyArg(),
			pgxmock.AnyArg(),
		).
		WillReturnResult(pgxmock.NewResult("UPDATE", 1))

	if err := repo.MarkFailed(ctx, job, update); err != nil {
		t.Fatalf("MarkFailed returned error: %v", err)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

func jobColumnNames() []string {
	return []string{
		"job_id",
		"document_id",
		"source_bucket",
		"source_key",
		"source_etag",
		"target_bucket",
		"target_key",
		"callback_url",
		"correlation_id",
		"target_media_type",
		"status",
		"attempts",
		"max_attempts",
		"next_run_at",
		"started_at",
		"finished_at",
		"error_code",
		"error_message",
		"error_details",
		"created_at",
		"updated_at",
	}
}
