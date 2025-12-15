package postgres

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"

	"backend-document-service/internal/domain"
)

func TestCreateDocument(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	repo := New(db)
	ctx := context.Background()

	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO documents")).
		WithArgs(sqlmock.AnyArg(), "file.docx", "", "", "", "", "", string(domain.DocumentStatusUploaded), "", "orig", "orig/key", "md", "md/key", "", false, sqlmock.AnyArg(), sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO document_versions")).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO document_status_logs")).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()

	doc := &domain.Document{
		Name:              "file.docx",
		Status:            domain.DocumentStatusUploaded,
		OriginalBucket:    "orig",
		OriginalObjectKey: "orig/key",
		MarkdownBucket:    "md",
		MarkdownObjectKey: "md/key",
	}
	version := &domain.DocumentVersion{Version: 1, OriginalObjectKey: "orig/key", MarkdownObjectKey: "md/key"}
	log := &domain.DocumentStatusLog{ToStatus: domain.DocumentStatusUploaded}

	saved, err := repo.CreateDocument(ctx, doc, version, log)
	require.NoError(t, err)
	require.NotEmpty(t, saved.ID)
	require.Len(t, saved.Versions, 1)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestUpdateStatusFlow(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	repo := New(db)
	ctx := context.Background()

	now := time.Now()
	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta("SELECT id, name, owner, repository, branch, base_branch, path, status, workflow_id, original_bucket, original_object_key, markdown_bucket, markdown_object_key, error_reason, create_pr, created_at, updated_at FROM documents WHERE id=$1 FOR UPDATE")).
		WithArgs("doc-1").
		WillReturnRows(sqlmock.NewRows([]string{"id", "name", "owner", "repository", "branch", "base_branch", "path", "status", "workflow_id", "original_bucket", "original_object_key", "markdown_bucket", "markdown_object_key", "error_reason", "create_pr", "created_at", "updated_at"}).
			AddRow("doc-1", "name", "", "", "", "", "", domain.DocumentStatusUploaded, "", "orig", "orig/key", "md", "md/key", "", false, now, now))
	mock.ExpectQuery("SELECT id, document_id, version").
		WithArgs("doc-1", int32(2)).
		WillReturnError(sql.ErrNoRows)
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO document_versions")).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec(regexp.QuoteMeta("UPDATE documents SET status=$1, original_object_key=$2, markdown_object_key=$3, error_reason=$4, updated_at=$5 WHERE id=$6")).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO document_status_logs")).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectQuery("SELECT id, document_id, version").
		WithArgs("doc-1").
		WillReturnRows(sqlmock.NewRows([]string{"id", "document_id", "version", "original_object_key", "markdown_object_key", "markdown_queue_id", "created_at", "updated_at"}).
			AddRow("ver-1", "doc-1", 1, "orig/key", "md/key", "", now, now).
			AddRow("ver-2", "doc-1", 2, "orig/key2", "md/key2", "", now, now))
	mock.ExpectCommit()
	mock.ExpectQuery(regexp.QuoteMeta("SELECT task_id, document_id, stage, status, attempts, next_run_at, payload, created_at, updated_at FROM processing_jobs WHERE document_id=$1 AND status IN ($2,$3) ORDER BY created_at DESC LIMIT 1")).
		WithArgs("doc-1", domain.JobStatusPending, domain.JobStatusInProgress).
		WillReturnError(sql.ErrNoRows)

	version := &domain.DocumentVersion{Version: 2, OriginalObjectKey: "orig/key2", MarkdownObjectKey: "md/key2"}
	doc, err := repo.UpdateStatus(ctx, "doc-1", domain.DocumentStatusStoredOriginal, "stored", "", version)
	require.NoError(t, err)
	require.Equal(t, domain.DocumentStatusStoredOriginal, doc.Status)
	require.Len(t, doc.Versions, 2)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestUpdateStatusIdempotent(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	repo := New(db)
	ctx := context.Background()

	now := time.Now()
	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta("SELECT id, name, owner, repository, branch, base_branch, path, status, workflow_id, original_bucket, original_object_key, markdown_bucket, markdown_object_key, error_reason, create_pr, created_at, updated_at FROM documents WHERE id=$1 FOR UPDATE")).
		WithArgs("doc-1").
		WillReturnRows(sqlmock.NewRows([]string{"id", "name", "owner", "repository", "branch", "base_branch", "path", "status", "workflow_id", "original_bucket", "original_object_key", "markdown_bucket", "markdown_object_key", "error_reason", "create_pr", "created_at", "updated_at"}).
			AddRow("doc-1", "name", "", "", "", "", "", domain.DocumentStatusStoredOriginal, "wf", "orig", "orig/key2", "md", "md/key2", "", false, now, now))
	mock.ExpectQuery("SELECT id, document_id, version").
		WithArgs("doc-1", int32(2)).
		WillReturnRows(sqlmock.NewRows([]string{"id", "document_id", "version", "original_object_key", "markdown_object_key", "markdown_queue_id", "created_at", "updated_at"}).
			AddRow("ver-2", "doc-1", 2, "orig/key2", "md/key2", "", now, now))
	mock.ExpectQuery("SELECT id, document_id, version").
		WithArgs("doc-1").
		WillReturnRows(sqlmock.NewRows([]string{"id", "document_id", "version", "original_object_key", "markdown_object_key", "markdown_queue_id", "created_at", "updated_at"}).
			AddRow("ver-1", "doc-1", 1, "orig/key", "md/key", "", now, now).
			AddRow("ver-2", "doc-1", 2, "orig/key2", "md/key2", "", now, now))
	mock.ExpectCommit()
	mock.ExpectQuery(regexp.QuoteMeta("SELECT task_id, document_id, stage, status, attempts, next_run_at, payload, created_at, updated_at FROM processing_jobs WHERE document_id=$1 AND status IN ($2,$3) ORDER BY created_at DESC LIMIT 1")).
		WithArgs("doc-1", domain.JobStatusPending, domain.JobStatusInProgress).
		WillReturnError(sql.ErrNoRows)

	version := &domain.DocumentVersion{Version: 2, OriginalObjectKey: "orig/key2", MarkdownObjectKey: "md/key2"}
	doc, err := repo.UpdateStatus(ctx, "doc-1", domain.DocumentStatusStoredOriginal, "", "", version)
	require.NoError(t, err)
	require.Equal(t, domain.DocumentStatusStoredOriginal, doc.Status)
	require.Len(t, doc.Versions, 2)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestUpdateStatusVersionConflict(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	repo := New(db)
	ctx := context.Background()

	now := time.Now()
	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta("SELECT id, name, owner, repository, branch, base_branch, path, status, workflow_id, original_bucket, original_object_key, markdown_bucket, markdown_object_key, error_reason, create_pr, created_at, updated_at FROM documents WHERE id=$1 FOR UPDATE")).
		WithArgs("doc-1").
		WillReturnRows(sqlmock.NewRows([]string{"id", "name", "owner", "repository", "branch", "base_branch", "path", "status", "workflow_id", "original_bucket", "original_object_key", "markdown_bucket", "markdown_object_key", "error_reason", "create_pr", "created_at", "updated_at"}).
			AddRow("doc-1", "name", "", "", "", "", "", domain.DocumentStatusStoredOriginal, "wf", "orig", "orig/key", "md", "md/key", "", false, now, now))
	mock.ExpectQuery("SELECT id, document_id, version").
		WithArgs("doc-1", int32(2)).
		WillReturnRows(sqlmock.NewRows([]string{"id", "document_id", "version", "original_object_key", "markdown_object_key", "markdown_queue_id", "created_at", "updated_at"}).
			AddRow("ver-2", "doc-1", 2, "orig/other", "md/other", "", now, now))
	mock.ExpectRollback()

	_, err = repo.UpdateStatus(ctx, "doc-1", domain.DocumentStatusStoredOriginal, "", "", &domain.DocumentVersion{Version: 2, OriginalObjectKey: "orig/key2", MarkdownObjectKey: "md/key2"})
	require.Error(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestListDocuments(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	repo := New(db)
	ctx := context.Background()

	mock.ExpectQuery(regexp.QuoteMeta("SELECT id, name, owner, repository, branch, base_branch, path, status, workflow_id, original_bucket, original_object_key, markdown_bucket, markdown_object_key, error_reason, create_pr, created_at, updated_at FROM documents ORDER BY created_at DESC")).
		WillReturnRows(sqlmock.NewRows([]string{"id", "name", "owner", "repository", "branch", "base_branch", "path", "status", "workflow_id", "original_bucket", "original_object_key", "markdown_bucket", "markdown_object_key", "error_reason", "create_pr", "created_at", "updated_at"}).
			AddRow("doc-1", "name", "", "", "", "", "", domain.DocumentStatusUploaded, "", "orig", "orig/key", "md", "md/key", "", false, time.Now(), time.Now()))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT task_id, document_id, stage, status, attempts, next_run_at, payload, created_at, updated_at FROM processing_jobs WHERE document_id=$1 AND status IN ($2,$3) ORDER BY created_at DESC LIMIT 1")).
		WithArgs("doc-1", domain.JobStatusPending, domain.JobStatusInProgress).
		WillReturnError(sql.ErrNoRows)

	docs, err := repo.ListDocuments(ctx, 0, 0, nil)
	require.NoError(t, err)
	require.Len(t, docs, 1)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestListFinishedJobs(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	repo := New(db)
	ctx := context.Background()

	now := time.Now()
	postpone := 5 * time.Minute

	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT task_id, document_id, stage, status, attempts, next_run_at, payload, created_at, updated_at
FROM processing_jobs
WHERE status IN ($1,$2) AND next_run_at <= $3
ORDER BY next_run_at
LIMIT $4
FOR UPDATE SKIP LOCKED`)).
		WithArgs(domain.JobStatusCompleted, domain.JobStatusFailed, sqlmock.AnyArg(), 10).
		WillReturnRows(sqlmock.NewRows([]string{"task_id", "document_id", "stage", "status", "attempts", "next_run_at", "payload", "created_at", "updated_at"}).
			AddRow("job-1", "doc-1", domain.JobStageStoreOriginal, domain.JobStatusCompleted, 1, now, json.RawMessage(`{"foo":"bar"}`), now, now))

	mock.ExpectExec(regexp.QuoteMeta("UPDATE processing_jobs SET next_run_at=$1, updated_at=$2 WHERE task_id=$3")).
		WithArgs(sqlmock.AnyArg(), sqlmock.AnyArg(), "job-1").
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()

	jobs, err := repo.ListFinishedJobs(ctx, 10, postpone)
	require.NoError(t, err)
	require.Len(t, jobs, 1)
	require.Equal(t, "doc-1", jobs[0].DocumentID)
	require.Equal(t, domain.JobStageStoreOriginal, jobs[0].Stage)
	require.Equal(t, "bar", jobs[0].Payload["foo"])
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestAcquirePendingJobs(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	repo := New(db)
	ctx := context.Background()

	now := time.Now()
	rows := sqlmock.NewRows([]string{"task_id", "document_id", "stage", "status", "attempts", "next_run_at", "payload", "created_at", "updated_at"}).
		AddRow("task-1", "doc-1", domain.JobStageStoreOriginal, domain.JobStatusPending, 0, now, json.RawMessage(`{"foo":"bar"}`), now.Add(-time.Hour), now.Add(-time.Hour))

	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT task_id, document_id, stage, status, attempts, next_run_at, payload, created_at, updated_at
FROM processing_jobs
WHERE stage=$1 AND status=$2 AND next_run_at <= $3
ORDER BY next_run_at
LIMIT $4
FOR UPDATE SKIP LOCKED`)).
		WithArgs(domain.JobStageStoreOriginal, domain.JobStatusPending, sqlmock.AnyArg(), 5).
		WillReturnRows(rows)
	mock.ExpectExec(regexp.QuoteMeta("UPDATE processing_jobs SET status=$1, attempts=$2, updated_at=$3, payload=$4 WHERE task_id=$5")).
		WithArgs(domain.JobStatusInProgress, 1, sqlmock.AnyArg(), json.RawMessage(`{"foo":"bar"}`), "task-1").
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()

	jobs, err := repo.AcquirePendingJobs(ctx, domain.JobStageStoreOriginal, 5)
	require.NoError(t, err)
	require.Len(t, jobs, 1)
	require.Equal(t, domain.JobStatusInProgress, jobs[0].Status)
	require.Equal(t, 1, jobs[0].Attempts)
	require.Equal(t, "bar", jobs[0].Payload["foo"])
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestRequeueStaleProcessingJobs_RetryAfterBadConnection(t *testing.T) {
	db, mock, err := sqlmock.New(sqlmock.MonitorPingsOption(true))
	require.NoError(t, err)
	defer db.Close()

	repo := New(db)
	ctx := context.Background()

	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT task_id, document_id, stage, status, attempts, next_run_at, payload, created_at, updated_at
FROM processing_jobs
WHERE status=$1 AND updated_at <= $2
ORDER BY updated_at
LIMIT $3
FOR UPDATE SKIP LOCKED`)).
		WithArgs(domain.JobStatusInProgress, sqlmock.AnyArg(), 1).
		WillReturnError(driver.ErrBadConn)
	mock.ExpectRollback()

	mock.ExpectPing()

	now := time.Now()
	rows := sqlmock.NewRows([]string{"task_id", "document_id", "stage", "status", "attempts", "next_run_at", "payload", "created_at", "updated_at"}).
		AddRow("task-1", "doc-1", domain.JobStageStoreOriginal, domain.JobStatusInProgress, 0, now, json.RawMessage(`{"foo":"bar"}`), now.Add(-time.Minute), now.Add(-time.Minute))

	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT task_id, document_id, stage, status, attempts, next_run_at, payload, created_at, updated_at
FROM processing_jobs
WHERE status=$1 AND updated_at <= $2
ORDER BY updated_at
LIMIT $3
FOR UPDATE SKIP LOCKED`)).
		WithArgs(domain.JobStatusInProgress, sqlmock.AnyArg(), 1).
		WillReturnRows(rows)
	mock.ExpectExec(regexp.QuoteMeta("UPDATE processing_jobs SET status=$1, attempts=$2, next_run_at=$3, payload=$4, updated_at=$5 WHERE task_id=$6")).
		WithArgs(domain.JobStatusPending, 1, sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), "task-1").
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO job_events (id, job_id, stage, status, event_type, message, payload, created_at) VALUES ($1,$2,$3,$4,$5,$6,$7,$8)")).
		WithArgs(sqlmock.AnyArg(), "task-1", domain.JobStageStoreOriginal, domain.JobStatusPending, "recovered", sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()

	jobs, err := repo.RequeueStaleProcessingJobs(ctx, time.Now(), 1, time.Now().Add(time.Minute), "restored")
	require.NoError(t, err)
	require.Len(t, jobs, 1)
	require.Equal(t, "task-1", jobs[0].TaskID)
	require.Equal(t, domain.JobStatusPending, jobs[0].Status)
	require.Equal(t, 1, jobs[0].Attempts)
	require.NoError(t, mock.ExpectationsWereMet())
}
