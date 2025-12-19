package repository

import (
	"context"
	"database/sql"
	"errors"
	"os/exec"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/mono-repo/docling-transformer-service/db/migrations"
	transformations "github.com/mono-repo/docling-transformer-service/internal/domain/transformations"
	repo "github.com/mono-repo/docling-transformer-service/internal/repository"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
)

type TransformationStatus = transformations.TransformationStatus

func startPostgresContainer(t *testing.T) (context.Context, *pgxpool.Pool) {
	t.Helper()

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
		_ = pgContainer.Terminate(context.Background())
	})

	dsn, err := pgContainer.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err)

	db, err := sql.Open("pgx", dsn)
	require.NoError(t, err)
	db.SetMaxOpenConns(1)
	t.Cleanup(func() {
		_ = db.Close()
	})

	require.Eventually(t, func() bool {
		return db.PingContext(ctx) == nil
	}, 10*time.Second, 300*time.Millisecond)

	require.NoError(t, migrations.Run(ctx, db))

	pool, err := pgxpool.New(ctx, dsn)
	require.NoError(t, err)
	t.Cleanup(func() {
		pool.Close()
	})

	return ctx, pool
}

func TestTransformationsQueueHasNoOverlayColumns(t *testing.T) {
	ctx, pool := startPostgresContainer(t)

	const query = `
SELECT EXISTS (
    SELECT 1
    FROM information_schema.columns
    WHERE table_schema = 'public'
      AND table_name = 'transformations_queue'
      AND column_name = $1
)`

	for _, column := range []string{"overlay_stage", "base_markdown_bucket", "base_markdown_object_key"} {
		var exists bool
		require.NoError(t, pool.QueryRow(ctx, query, column).Scan(&exists))
		require.Falsef(t, exists, "column %s should not exist", column)
	}
}

func TestRestoreStuckProcessingResetsDoclingState(t *testing.T) {
	ctx, pool := startPostgresContainer(t)

	repo := NewQueueRepository(pool)
	var err error

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
	require.Equal(t, "", task.DoclingTaskID)
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

func TestQueueRepositoryPersistsDoclingIdentifiers(t *testing.T) {
	ctx, pool := startPostgresContainer(t)

	repo := NewQueueRepository(pool)

	queueID, err := repo.Enqueue(ctx, transformations.EnqueueParams{
		DocumentID:        "doc-id",
		OriginalBucket:    "orig",
		OriginalObjectKey: "path/file.docx",
		MarkdownBucket:    "md",
		MarkdownObjectKey: "dest/file.md",
		FilenameExtension: "docx",
		CorrelationID:     "corr-123",
	})
	require.NoError(t, err)

	require.NoError(t, repo.UpdateDoclingInfo(ctx, queueID, "task-123", "job-456", 2))

	task, err := repo.GetByID(ctx, queueID)
	require.NoError(t, err)
	require.NotNil(t, task)
	require.Equal(t, "task-123", task.DoclingTaskID)
	require.Equal(t, "job-456", task.DoclingJobID)
	require.Equal(t, 2, task.DoclingPollCount)

	nextPoll := time.Now().Add(time.Minute)
	require.NoError(t, repo.MarkDoclingWaiting(ctx, queueID, "task-123", "job-456", 3, nextPoll))

	task, err = repo.GetByID(ctx, queueID)
	require.NoError(t, err)
	require.NotNil(t, task)
	require.Equal(t, StatusDocling, task.Status)
	require.Equal(t, "task-123", task.DoclingTaskID)
	require.Equal(t, "job-456", task.DoclingJobID)
	require.Equal(t, 3, task.DoclingPollCount)
	require.NotNil(t, task.NextPollAt)
	require.WithinDuration(t, nextPoll, *task.NextPollAt, time.Second)

	require.NoError(t, repo.MarkDone(ctx, queueID, "md", "dest/other.md"))

	task, err = repo.GetByID(ctx, queueID)
	require.NoError(t, err)
	require.NotNil(t, task)
	require.Equal(t, StatusDone, task.Status)
	require.Nil(t, task.NextPollAt)
	require.Equal(t, "md", task.MarkdownBucket)
	require.Equal(t, "dest/other.md", task.MarkdownObjectKey)
}

func TestRestoreProcessingMovesTasksToPendingOnStartup(t *testing.T) {
	ctx, pool := startPostgresContainer(t)

	repo := NewQueueRepository(pool)
	var err error

	startedAt := time.Now().Add(-2 * time.Minute)
	taskID := uuid.New()
	_, err = pool.Exec(ctx, `
INSERT INTO transformations_queue (
    id, status, document_id, original_bucket, original_object_key, markdown_bucket, markdown_object_key, filename_extension,
    created_at, started_at, retry_count, correlation_id
) VALUES ($1, 'processing', 'doc-start', 'orig', 'path/start.docx', 'md', 'dest/start.md', 'docx', $2, $3, 1, 'corr-start')
`, taskID, startedAt.Add(-time.Minute), startedAt)
	require.NoError(t, err)

	restored, err := repo.RestoreProcessing(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, restored)

	var status TransformationStatus
	var started *time.Time
	var retryCount int
	var errorMessage string
	err = pool.QueryRow(ctx, `SELECT status, started_at, retry_count, error FROM transformations_queue WHERE id = $1`, taskID).
		Scan(&status, &started, &retryCount, &errorMessage)
	require.NoError(t, err)
	require.Equal(t, StatusPending, status)
	require.Nil(t, started)
	require.Equal(t, 1, retryCount)
	require.Equal(t, "", errorMessage)

	task, err := repo.AcquireNext(ctx)
	require.NoError(t, err)
	require.NotNil(t, task)
	require.Equal(t, taskID, task.ID)
	require.Equal(t, StatusProcessing, task.Status)
}

func TestRetryFailedMovesTasksToPending(t *testing.T) {
	ctx, pool := startPostgresContainer(t)

	repo := NewQueueRepository(pool)
	var err error

	finishedAt := time.Now().Add(-2 * time.Hour)
	retryID := uuid.New()
	recentID := uuid.New()
	exhaustedID := uuid.New()

	_, err = pool.Exec(ctx, `
INSERT INTO transformations_queue (
    id, status, document_id, original_bucket, original_object_key, markdown_bucket, markdown_object_key, filename_extension,
    created_at, finished_at, retry_count, correlation_id, error, docling_job_id, docling_poll_count
) VALUES
    ($1, 'failed', 'doc-retry', 'orig', 'path/retry.docx', 'md', 'dest/retry.md', 'docx', $4, $2, 1, 'corr-retry', 'tmp error', 'job-1', 2),
    ($3, 'failed', 'doc-recent', 'orig', 'path/recent.docx', 'md', 'dest/recent.md', 'docx', $4, NOW(), 1, 'corr-recent', 'recent fail', 'job-2', 1),
    ($5, 'failed', 'doc-exhaust', 'orig', 'path/exhaust.docx', 'md', 'dest/exhaust.md', 'docx', $4, $2, 5, 'corr-exhaust', 'old fail', 'job-3', 1)
`, retryID, finishedAt, recentID, finishedAt.Add(-time.Minute), exhaustedID)
	require.NoError(t, err)

	retried, exhausted, err := repo.RetryFailed(ctx, time.Now().Add(-time.Hour), 3)
	require.NoError(t, err)
	require.Equal(t, 1, retried)
	require.Equal(t, 1, exhausted)

	var status TransformationStatus
	var startedAt *time.Time
	var finishedAtPtr *time.Time
	var doclingJob string
	var doclingPolls int
	var taskError *string

	err = pool.QueryRow(ctx, `SELECT status, started_at, finished_at, error, docling_job_id, docling_poll_count FROM transformations_queue WHERE id = $1`, retryID).
		Scan(&status, &startedAt, &finishedAtPtr, &taskError, &doclingJob, &doclingPolls)
	require.NoError(t, err)
	require.Equal(t, StatusPending, status)
	require.Nil(t, startedAt)
	require.Nil(t, finishedAtPtr)
	require.Nil(t, taskError)
	require.Equal(t, "", doclingJob)
	require.Equal(t, 0, doclingPolls)

	var recentStatus TransformationStatus
	require.NoError(t, pool.QueryRow(ctx, `SELECT status FROM transformations_queue WHERE id = $1`, recentID).Scan(&recentStatus))
	require.Equal(t, StatusFailed, recentStatus)

	var exhaustedStatus TransformationStatus
	var exhaustedError string
	require.NoError(t, pool.QueryRow(ctx, `SELECT status, error FROM transformations_queue WHERE id = $1`, exhaustedID).Scan(&exhaustedStatus, &exhaustedError))
	require.Equal(t, StatusFailed, exhaustedStatus)
	require.Contains(t, exhaustedError, "retry limit exceeded (5/3)")
	require.Contains(t, exhaustedError, "old fail")
}

func TestAcquireBatchPrioritizesWaitingDocling(t *testing.T) {
	ctx, pool := startPostgresContainer(t)

	repo := NewQueueRepository(pool)
	now := time.Now()

	waitingFirst := uuid.New()
	waitingSecond := uuid.New()
	pendingID := uuid.New()

	_, err := pool.Exec(ctx, `
INSERT INTO transformations_queue (
    id, status, document_id, original_bucket, original_object_key, markdown_bucket, markdown_object_key, filename_extension,
    created_at, correlation_id, docling_job_id, docling_poll_count, next_poll_at
) VALUES ($1, 'waiting_docling', 'doc-10', 'orig', 'path/first.docx', 'md', 'dest/first.md', 'docx', $4, 'corr-wait-1', 'job-1', 1, $2),
         ($3, 'waiting_docling', 'doc-11', 'orig', 'path/second.docx', 'md', 'dest/second.md', 'docx', $5, 'corr-wait-2', 'job-2', 1, $6),
         ($7, 'pending', 'doc-12', 'orig', 'path/pending.docx', 'md', 'dest/pending.md', 'docx', $5, 'corr-pending', NULL, 0, NULL)
`, waitingFirst, now.Add(-2*time.Minute), waitingSecond, now.Add(-time.Minute), now.Add(-90*time.Second), now.Add(-30*time.Second), pendingID)
	require.NoError(t, err)

	tasks, err := repo.AcquireBatch(ctx, 5)
	require.NoError(t, err)
	require.Len(t, tasks, 1)
	require.Equal(t, waitingFirst, tasks[0].ID)
	require.Equal(t, StatusProcessing, tasks[0].Status)

	var secondStatus TransformationStatus
	require.NoError(t, pool.QueryRow(ctx, `SELECT status FROM transformations_queue WHERE id = $1`, waitingSecond).Scan(&secondStatus))
	require.Equal(t, StatusDocling, secondStatus)

	var pendingStatus TransformationStatus
	require.NoError(t, pool.QueryRow(ctx, `SELECT status FROM transformations_queue WHERE id = $1`, pendingID).Scan(&pendingStatus))
	require.Equal(t, StatusPending, pendingStatus)

	tasks, err = repo.AcquireBatch(ctx, 5)
	require.NoError(t, err)
	require.Len(t, tasks, 1)
	require.Equal(t, waitingSecond, tasks[0].ID)

	tasks, err = repo.AcquireBatch(ctx, 5)
	require.NoError(t, err)
	require.Len(t, tasks, 1)
	require.Equal(t, pendingID, tasks[0].ID)
}

func TestAcquireBatchDoesNotStartPendingWhileWaitingScheduled(t *testing.T) {
	ctx, pool := startPostgresContainer(t)

	repo := NewQueueRepository(pool)

	waitingID := uuid.New()
	pendingID := uuid.New()

	_, err := pool.Exec(ctx, `
INSERT INTO transformations_queue (
    id, status, document_id, original_bucket, original_object_key, markdown_bucket, markdown_object_key, filename_extension,
    created_at, correlation_id, docling_job_id, docling_poll_count, next_poll_at
) VALUES ($1, 'waiting_docling', 'doc-20', 'orig', 'path/waiting.docx', 'md', 'dest/waiting.md', 'docx', NOW(), 'corr-wait', 'job-20', 1, NOW() + INTERVAL '5 minutes'),
         ($2, 'pending', 'doc-21', 'orig', 'path/pending.docx', 'md', 'dest/pending.md', 'docx', NOW(), 'corr-pending', NULL, 0, NULL)
`, waitingID, pendingID)
	require.NoError(t, err)

	tasks, err := repo.AcquireBatch(ctx, 5)
	require.NoError(t, err)
	require.Empty(t, tasks)

	var waitingStatus TransformationStatus
	require.NoError(t, pool.QueryRow(ctx, `SELECT status FROM transformations_queue WHERE id = $1`, waitingID).Scan(&waitingStatus))
	require.Equal(t, StatusDocling, waitingStatus)

	var pendingStatus TransformationStatus
	require.NoError(t, pool.QueryRow(ctx, `SELECT status FROM transformations_queue WHERE id = $1`, pendingID).Scan(&pendingStatus))
	require.Equal(t, StatusPending, pendingStatus)
}

func TestAcquireBatchSkipsPendingWithFutureNextPoll(t *testing.T) {
	ctx, pool := startPostgresContainer(t)

	repo := NewQueueRepository(pool)
	now := time.Now()
	delayedID := uuid.New()
	readyID := uuid.New()

	_, err := pool.Exec(ctx, `
INSERT INTO transformations_queue (
    id, status, document_id, original_bucket, original_object_key, markdown_bucket, markdown_object_key, filename_extension,
    created_at, retry_count, correlation_id, next_poll_at
) VALUES ($1, 'pending', 'doc-delayed', 'orig', 'path/delayed.docx', 'md', 'dest/delayed.md', 'docx', $2, 1, 'corr-delayed', $3),
         ($4, 'pending', 'doc-ready', 'orig', 'path/ready.docx', 'md', 'dest/ready.md', 'docx', $5, 1, 'corr-ready', NULL)
`, delayedID, now.Add(-time.Hour), now.Add(time.Hour), readyID, now)
	require.NoError(t, err)

	tasks, err := repo.AcquireBatch(ctx, 1)
	require.NoError(t, err)
	require.Len(t, tasks, 1)
	require.Equal(t, readyID, tasks[0].ID)

	var delayedStatus TransformationStatus
	require.NoError(t, pool.QueryRow(ctx, `SELECT status FROM transformations_queue WHERE id = $1`, delayedID).Scan(&delayedStatus))
	require.Equal(t, StatusPending, delayedStatus)
}

func TestAcquireSubmitLockIsExclusive(t *testing.T) {
	ctx, pool := startPostgresContainer(t)

	queueRepo := NewQueueRepository(pool)

	lock, err := queueRepo.AcquireSubmitLock(ctx)
	require.NoError(t, err)
	require.NotNil(t, lock)

	lockedResult := make(chan error, 1)

	go func() {
		_, err := queueRepo.AcquireSubmitLock(ctx)
		if !errors.Is(err, repo.ErrSubmitLockBusy) {
			lockedResult <- err
			return
		}
		lockedResult <- nil
	}()

	require.Eventually(t, func() bool {
		return len(lockedResult) > 0
	}, 2*time.Second, 50*time.Millisecond)

	require.NoError(t, <-lockedResult)

	require.NoError(t, lock.Release(ctx))

	lockAgain, err := queueRepo.AcquireSubmitLock(ctx)
	require.NoError(t, err)
	require.NotNil(t, lockAgain)
	require.NoError(t, lockAgain.Release(ctx))
}
