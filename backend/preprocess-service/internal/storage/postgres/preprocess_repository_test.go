package postgres

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/pashagolub/pgxmock/v3"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"

	"preprocess-service/internal/domain"
)

func TestPreprocessRepository_NextForProcessingLocksAndMarksProcessing(t *testing.T) {
	ctx := context.Background()
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatalf("create mock pool: %v", err)
	}
	defer mock.Close()

	core, logs := observer.New(zap.DebugLevel)
	repo := NewPreprocessRepository(mock, zap.New(core))

	now := time.Now().UTC()
	job := domain.PreprocessJob{
		ID:            "job-next",
		DocumentID:    "doc-1",
		SourceBucket:  "source",
		SourceKey:     "input.txt",
		SourceETag:    "etag-1",
		CorrelationID: "corr-123",
		Status:        domain.StatusPending,
		Attempts:      0,
		MaxAttempts:   3,
		NextRunAt:     now.Add(-time.Minute),
		CreatedAt:     now.Add(-2 * time.Minute),
		UpdatedAt:     now.Add(-time.Minute),
	}

	rows := pgxmock.NewRows(jobColumnNames()).
		AddRow(job.ID, job.DocumentID, job.SourceBucket, job.SourceKey, job.SourceETag, nil, nil, nil, nullableString(job.CorrelationID), nil,
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

	entries := logs.FilterMessage("preprocess job picked for processing").All()
	if len(entries) != 1 {
		t.Fatalf("expected single log entry, got %d", len(entries))
	}
	logFields := entries[0].ContextMap()
	if got := logFields["job_id"]; got != job.ID {
		t.Fatalf("expected job_id %s, got %v", job.ID, got)
	}
	if got := logFields["status_from"]; got != job.Status.String() {
		t.Fatalf("expected status_from %s, got %v", job.Status, got)
	}
	if got := logFields["status_to"]; got != domain.StatusProcessing.String() {
		t.Fatalf("expected status_to %s, got %v", domain.StatusProcessing, got)
	}
	if got := readIntField(logFields["attempts"]); got != picked.Attempts {
		t.Fatalf("expected attempts %d in log, got %v", picked.Attempts, got)
	}
	if got := readTimeField(logFields["next_run_at"]); !got.Equal(job.NextRunAt) {
		t.Fatalf("expected next_run_at %v, got %v", job.NextRunAt, got)
	}
	if got := logFields["correlation_id"]; got != job.CorrelationID {
		t.Fatalf("expected correlation_id %s, got %v", job.CorrelationID, got)
	}
}

func TestPreprocessRepository_RequeueStuckMakesProcessingAvailable(t *testing.T) {
	ctx := context.Background()
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatalf("create mock pool: %v", err)
	}
	defer mock.Close()

	core, logs := observer.New(zap.DebugLevel)
	repo := NewPreprocessRepository(mock, zap.New(core))

	now := time.Now().UTC()
	convertTimeout := 2 * time.Minute
	backoff := 30 * time.Second

	job := domain.PreprocessJob{
		ID:           "job-stuck",
		DocumentID:   "doc-stuck",
		SourceBucket: "bucket",
		SourceKey:    "key",
		SourceETag:   "etag",
		Status:       domain.StatusProcessing,
		Attempts:     1,
		MaxAttempts:  3,
		NextRunAt:    now.Add(-time.Minute),
		StartedAt:    now.Add(-(convertTimeout + backoff + time.Minute)),
		CreatedAt:    now.Add(-2 * time.Minute),
		UpdatedAt:    now.Add(-time.Minute),
	}

	requeuedAttempts := job.Attempts + 1
	availableAt := now.Add(-time.Second)

	mock.ExpectQuery("UPDATE preprocess_queue").
		WithArgs(
			toStatusString(domain.StatusFailed),
			pgxmock.AnyArg(),
			pgxmock.AnyArg(),
			toStatusString(domain.StatusProcessing),
			pgxmock.AnyArg(),
		).
		WillReturnRows(pgxmock.NewRows([]string{"job_id", "correlation_id", "attempts", "next_run_at"}).
			AddRow(job.ID, nullableString("corr-requeued"), requeuedAttempts, availableAt))

	if err := repo.RequeueStuck(ctx, convertTimeout, backoff); err != nil {
		t.Fatalf("RequeueStuck returned error: %v", err)
	}

	rows := pgxmock.NewRows(jobColumnNames()).
		AddRow(job.ID, job.DocumentID, job.SourceBucket, job.SourceKey, job.SourceETag, nil, nil, nil, nullableString("corr-requeued"), nil,
			toStatusString(domain.StatusFailed), requeuedAttempts, job.MaxAttempts, availableAt, &job.StartedAt, nil, nil, nil, []byte("{}"), job.CreatedAt, job.UpdatedAt)

	mock.ExpectBeginTx(pgx.TxOptions{})
	mock.ExpectQuery("SELECT .*FOR UPDATE SKIP LOCKED").
		WithArgs(toStatusString(domain.StatusPending), toStatusString(domain.StatusFailed)).
		WillReturnRows(rows)
	mock.ExpectExec("UPDATE preprocess_queue").
		WithArgs(
			toStatusString(domain.StatusProcessing),
			requeuedAttempts+1,
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
		t.Fatal("expected job to be found after requeue")
	}
	if picked.Status != domain.StatusProcessing {
		t.Fatalf("expected status processing, got %s", picked.Status)
	}
	if picked.Attempts != requeuedAttempts+1 {
		t.Fatalf("expected attempts to increase to %d, got %d", requeuedAttempts+1, picked.Attempts)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}

	entries := logs.FilterMessage("requeued stuck preprocess job").All()
	if len(entries) != 1 {
		t.Fatalf("expected single log entry for requeue, got %d", len(entries))
	}
	logFields := entries[0].ContextMap()
	if got := logFields["job_id"]; got != job.ID {
		t.Fatalf("expected job_id %s, got %v", job.ID, got)
	}
	if got := logFields["status_from"]; got != domain.StatusProcessing.String() {
		t.Fatalf("expected status_from %s, got %v", domain.StatusProcessing, got)
	}
	if got := logFields["status_to"]; got != domain.StatusFailed.String() {
		t.Fatalf("expected status_to %s, got %v", domain.StatusFailed, got)
	}
	if got := readIntField(logFields["attempts"]); got != requeuedAttempts {
		t.Fatalf("expected attempts %d, got %v", requeuedAttempts, got)
	}
	if got := readTimeField(logFields["next_run_at"]); !got.Equal(availableAt) {
		t.Fatalf("expected next_run_at %v, got %v", availableAt, got)
	}
	if got := logFields["correlation_id"]; got != "corr-requeued" {
		t.Fatalf("expected correlation_id corr-requeued, got %v", got)
	}
}

func TestPreprocessRepository_MarkFailedUpdatesStatus(t *testing.T) {
	ctx := context.Background()
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatalf("create mock pool: %v", err)
	}
	defer mock.Close()

	core, logs := observer.New(zap.DebugLevel)
	repo := NewPreprocessRepository(mock, zap.New(core))

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

	entries := logs.FilterMessage("preprocess job marked failed").All()
	if len(entries) != 1 {
		t.Fatalf("expected single log entry for mark failed, got %d", len(entries))
	}
	logFields := entries[0].ContextMap()
	if got := logFields["job_id"]; got != job.ID {
		t.Fatalf("expected job_id %s, got %v", job.ID, got)
	}
	if got := logFields["status_from"]; got != job.Status.String() {
		t.Fatalf("expected status_from %s, got %v", job.Status, got)
	}
	if got := logFields["status_to"]; got != update.Status.String() {
		t.Fatalf("expected status_to %s, got %v", update.Status, got)
	}
	if got := readIntField(logFields["attempts"]); got != job.Attempts {
		t.Fatalf("expected attempts %d, got %v", job.Attempts, got)
	}
	if got := readTimeField(logFields["next_run_at"]); !got.Equal(update.NextRunAt) {
		t.Fatalf("expected next_run_at %v, got %v", update.NextRunAt, got)
	}
}

func TestPreprocessRepository_CreateLogsJob(t *testing.T) {
	ctx := context.Background()
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatalf("create mock pool: %v", err)
	}
	defer mock.Close()

	core, logs := observer.New(zap.DebugLevel)
	repo := NewPreprocessRepository(mock, zap.New(core))

	nextRunAt := time.Date(2024, 10, 1, 12, 0, 0, 0, time.UTC)
	job := domain.PreprocessJob{
		ID:            "job-create",
		DocumentID:    "doc-3",
		SourceBucket:  "source",
		SourceKey:     "input.txt",
		SourceETag:    "etag-3",
		CorrelationID: "corr-create",
		Status:        domain.StatusPending,
		Attempts:      1,
		MaxAttempts:   3,
		NextRunAt:     nextRunAt,
		CreatedAt:     nextRunAt,
		UpdatedAt:     nextRunAt,
	}

	mock.ExpectExec("INSERT INTO preprocess_queue").
		WithArgs(
			job.ID,
			job.DocumentID,
			job.SourceBucket,
			job.SourceKey,
			job.SourceETag,
			pgxmock.AnyArg(),
			pgxmock.AnyArg(),
			pgxmock.AnyArg(),
			nullableString(job.CorrelationID),
			pgxmock.AnyArg(),
			toStatusString(job.Status),
			job.Attempts,
			job.MaxAttempts,
			job.NextRunAt,
			pgxmock.AnyArg(),
			pgxmock.AnyArg(),
			pgxmock.AnyArg(),
			pgxmock.AnyArg(),
			`{}`,
			job.CreatedAt,
			job.UpdatedAt,
		).
		WillReturnResult(pgxmock.NewResult("INSERT", 1))

	if err := repo.Create(ctx, job); err != nil {
		t.Fatalf("Create returned error: %v", err)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}

	entries := logs.FilterMessage("preprocess job created").All()
	if len(entries) != 1 {
		t.Fatalf("expected single log entry for create, got %d", len(entries))
	}
	logFields := entries[0].ContextMap()
	if got := logFields["job_id"]; got != job.ID {
		t.Fatalf("expected job_id %s, got %v", job.ID, got)
	}
	if got := logFields["status_from"]; got != domain.StatusUnspecified.String() {
		t.Fatalf("expected status_from %s, got %v", domain.StatusUnspecified, got)
	}
	if got := logFields["status_to"]; got != job.Status.String() {
		t.Fatalf("expected status_to %s, got %v", job.Status, got)
	}
	if got := readIntField(logFields["attempts"]); got != job.Attempts {
		t.Fatalf("expected attempts %d, got %v", job.Attempts, got)
	}
	if got := readTimeField(logFields["next_run_at"]); !got.Equal(nextRunAt) {
		t.Fatalf("expected next_run_at %v, got %v", nextRunAt, got)
	}
	if got := logFields["correlation_id"]; got != job.CorrelationID {
		t.Fatalf("expected correlation_id %s, got %v", job.CorrelationID, got)
	}
}

func readIntField(value any) int {
	switch v := value.(type) {
	case int:
		return v
	case int32:
		return int(v)
	case int64:
		return int(v)
	default:
		return 0
	}
}

func readTimeField(value any) time.Time {
	if ts, ok := value.(time.Time); ok {
		return ts
	}
	return time.Time{}
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
