package repository

import (
	"context"
	"database/sql"
	"os/exec"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/mono-repo/docling-transformer-service/db/migrations"
	transformations "github.com/mono-repo/docling-transformer-service/internal/domain/transformations"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
)

type TransformationStatus = transformations.TransformationStatus

func TestRestoreStuckProcessingResetsDoclingState(t *testing.T) {
	ctx := context.Background()

	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker binary not available for testcontainers")
	}

	infoCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := exec.CommandContext(infoCtx, "docker", "info").Run(); err != nil {
		t.Skip("docker daemon not available for testcontainers")
	}

	pgContainer, err := postgres.RunContainer(ctx,
		testcontainers.WithImage("postgres:16-alpine"),
		postgres.WithDatabase("transformations"),
		postgres.WithUsername("postgres"),
		postgres.WithPassword("password"),
	)
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = pgContainer.Terminate(ctx)
	})

	dsn, err := pgContainer.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err)

	db, err := sql.Open("pgx", dsn)
	require.NoError(t, err)
	defer db.Close()
	db.SetMaxOpenConns(1)

	require.Eventually(t, func() bool {
		return db.PingContext(ctx) == nil
	}, 10*time.Second, 300*time.Millisecond)

	require.NoError(t, migrations.Run(ctx, db))

	pool, err := pgxpool.New(ctx, dsn)
	require.NoError(t, err)
	defer pool.Close()

	repo := NewQueueRepository(pool)

	startedAt := time.Now().Add(-5 * time.Minute)
	taskID := uuid.New()
	_, err = pool.Exec(ctx, `
INSERT INTO transformations_queue (
    id, status, document_id, original_bucket, original_object_key, markdown_bucket, markdown_object_key, filename_extension,
    created_at, started_at, retry_count, correlation_id, docling_job_id, docling_poll_count
) VALUES ($1, 'processing', 'doc-1', 'orig', 'path/file.docx', 'md', 'dest/file.md', 'docx', $2, $3, 1, 'corr', 'job-123', 1)
`, taskID, startedAt.Add(-time.Minute), startedAt)
	require.NoError(t, err)

	timeoutFailedID := uuid.New()
	_, err = pool.Exec(ctx, `
INSERT INTO transformations_queue (
    id, status, document_id, original_bucket, original_object_key, markdown_bucket, markdown_object_key, filename_extension,
    created_at, started_at, retry_count, correlation_id
) VALUES ($1, 'processing', 'doc-4', 'orig', 'path/fourth.docx', 'md', 'dest/fourth.md', 'docx', $2, $3, 5, 'corr-timeout')
`, timeoutFailedID, startedAt.Add(-2*time.Minute), startedAt.Add(-time.Minute))
	require.NoError(t, err)

	doclingFailedID := uuid.New()
	_, err = pool.Exec(ctx, `
INSERT INTO transformations_queue (
    id, status, document_id, original_bucket, original_object_key, markdown_bucket, markdown_object_key, filename_extension,
    created_at, started_at, retry_count, correlation_id, docling_job_id, docling_poll_count
) VALUES ($1, 'processing', 'doc-5', 'orig', 'path/fifth.docx', 'md', 'dest/fifth.md', 'docx', $2, $3, 1, 'corr-docling', 'docling-999', 3)
`, doclingFailedID, startedAt.Add(-3*time.Minute), startedAt.Add(-90*time.Second))
	require.NoError(t, err)

	restored, failed, err := repo.RestoreStuckProcessing(ctx, 30*time.Second, 3, 3)
	require.NoError(t, err)
	require.Equal(t, 1, restored)
	require.Equal(t, 2, failed)

	var status TransformationStatus
	var started *time.Time
	var jobID string
	var pollCount int
	var errorMessage string
	err = pool.QueryRow(ctx, `SELECT status, started_at, docling_job_id, docling_poll_count, error FROM transformations_queue WHERE id = $1`, taskID).
		Scan(&status, &started, &jobID, &pollCount, &errorMessage)
	require.NoError(t, err)
	require.Equal(t, StatusPending, status)
	require.Nil(t, started)
	require.Equal(t, "", jobID)
	require.Equal(t, 0, pollCount)
	require.Contains(t, errorMessage, "restored after timeout")
	require.Contains(t, errorMessage, "started_at")

	task, err := repo.AcquireNext(ctx)
	require.NoError(t, err)
	require.NotNil(t, task)
	require.Equal(t, taskID, task.ID)
	require.Equal(t, StatusProcessing, task.Status)
	require.Equal(t, "", task.DoclingJobID)
	require.Equal(t, 0, task.DoclingPollCount)

	var timeoutStatus TransformationStatus
	var timeoutError string
	require.NoError(t, pool.QueryRow(ctx, `SELECT status, error FROM transformations_queue WHERE id = $1`, timeoutFailedID).Scan(&timeoutStatus, &timeoutError))
	require.Equal(t, StatusFailed, timeoutStatus)
	require.Contains(t, timeoutError, "processing timeout exceeded")
	require.Contains(t, timeoutError, "retries=5/3")

	var doclingStatus TransformationStatus
	var doclingError string
	require.NoError(t, pool.QueryRow(ctx, `SELECT status, error FROM transformations_queue WHERE id = $1`, doclingFailedID).Scan(&doclingStatus, &doclingError))
	require.Equal(t, StatusFailed, doclingStatus)
	require.Contains(t, doclingError, "docling-999")
	require.Contains(t, doclingError, "3/3")

	waitingID := uuid.New()
	expiredID := uuid.New()
	now := time.Now()
	_, err = pool.Exec(ctx, `
INSERT INTO transformations_queue (
    id, status, document_id, original_bucket, original_object_key, markdown_bucket, markdown_object_key, filename_extension,
    created_at, correlation_id, docling_job_id, docling_poll_count, next_poll_at
) VALUES ($1, 'waiting_docling', 'doc-2', 'orig', 'path/another.docx', 'md', 'dest/another.md', 'docx', $4, 'corr-wait', 'job-456', 2, $2),
         ($3, 'waiting_docling', 'doc-3', 'orig', 'path/third.docx', 'md', 'dest/third.md', 'docx', $5, 'corr-exp', 'job-789', 5, $2)
`, waitingID, now.Add(-time.Minute), expiredID, now.Add(-30*time.Minute), now.Add(-3*time.Hour))
	require.NoError(t, err)

	ready, expired, err := repo.ActivateWaiting(ctx, time.Now(), time.Hour)
	require.NoError(t, err)
	require.Equal(t, 1, ready)
	require.Equal(t, 1, expired)

	var waitingStatus TransformationStatus
	require.NoError(t, pool.QueryRow(ctx, `SELECT status FROM transformations_queue WHERE id = $1`, waitingID).Scan(&waitingStatus))
	require.Equal(t, StatusPending, waitingStatus)

	var expiredStatus TransformationStatus
	require.NoError(t, pool.QueryRow(ctx, `SELECT status FROM transformations_queue WHERE id = $1`, expiredID).Scan(&expiredStatus))
	require.Equal(t, StatusFailed, expiredStatus)
}
