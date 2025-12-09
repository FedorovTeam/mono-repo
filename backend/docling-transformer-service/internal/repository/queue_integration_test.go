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
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
)

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
) VALUES ($1, 'processing', 'doc-1', 'orig', 'path/file.docx', 'md', 'dest/file.md', 'docx', $2, $3, 1, 'corr', 'job-123', 4)
`, taskID, startedAt.Add(-time.Minute), startedAt)
	require.NoError(t, err)

	restored, failed, err := repo.RestoreStuckProcessing(ctx, 30*time.Second, 3, 0)
	require.NoError(t, err)
	require.Equal(t, 1, restored)
	require.Equal(t, 0, failed)

	var status TransformationStatus
	var started *time.Time
	var jobID string
	var pollCount int
	err = pool.QueryRow(ctx, `SELECT status, started_at, docling_job_id, docling_poll_count FROM transformations_queue WHERE id = $1`, taskID).
		Scan(&status, &started, &jobID, &pollCount)
	require.NoError(t, err)
	require.Equal(t, StatusPending, status)
	require.Nil(t, started)
	require.Equal(t, "", jobID)
	require.Equal(t, 0, pollCount)

	task, err := repo.AcquireNext(ctx)
	require.NoError(t, err)
	require.NotNil(t, task)
	require.Equal(t, taskID, task.ID)
	require.Equal(t, StatusProcessing, task.Status)
	require.Equal(t, "", task.DoclingJobID)
	require.Equal(t, 0, task.DoclingPollCount)
}
