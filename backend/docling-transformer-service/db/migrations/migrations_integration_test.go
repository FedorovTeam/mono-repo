package migrations

import (
	"context"
	"database/sql"
	"os/exec"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
)

func TestRunMigrationsWithPostgres(t *testing.T) {
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

	require.NoError(t, Run(ctx, db))

	assertTableExists(t, ctx, db, "transformations_queue")

	columns := []struct {
		table  string
		column string
	}{
		{"transformations_queue", "id"},
		{"transformations_queue", "status"},
		{"transformations_queue", "document_id"},
		{"transformations_queue", "original_bucket"},
		{"transformations_queue", "original_object_key"},
		{"transformations_queue", "source_bucket"},
		{"transformations_queue", "source_object_key"},
		{"transformations_queue", "source_url"},
		{"transformations_queue", "markdown_bucket"},
		{"transformations_queue", "markdown_object_key"},
		{"transformations_queue", "filename_extension"},
		{"transformations_queue", "ocr_lang"},
		{"transformations_queue", "pdf_backend"},
		{"transformations_queue", "created_at"},
		{"transformations_queue", "started_at"},
		{"transformations_queue", "finished_at"},
		{"transformations_queue", "error"},
		{"transformations_queue", "retry_count"},
		{"transformations_queue", "correlation_id"},
		{"transformations_queue", "docling_task_id"},
		{"transformations_queue", "docling_job_id"},
		{"transformations_queue", "docling_poll_count"},
		{"transformations_queue", "next_poll_at"},
	}
	for _, c := range columns {
		assertColumnExists(t, ctx, db, c.table, c.column)
	}

	for _, c := range []struct {
		table  string
		column string
	}{
		{"transformations_queue", "overlay_stage"},
		{"transformations_queue", "base_markdown_bucket"},
		{"transformations_queue", "base_markdown_object_key"},
	} {
		assertColumnNotExists(t, ctx, db, c.table, c.column)
	}

	assertIndexExists(t, ctx, db, "transformations_queue_status_idx")
	assertIndexExists(t, ctx, db, "transformations_queue_started_at_idx")
	assertIndexExists(t, ctx, db, "transformations_queue_docling_idx")
	assertIndexExists(t, ctx, db, "transformations_queue_waiting_idx")
	assertIndexNotExists(t, ctx, db, "transformations_queue_overlay_stage_idx")
}

func assertTableExists(t *testing.T, ctx context.Context, db *sql.DB, name string) {
	t.Helper()
	var exists bool
	row := db.QueryRowContext(ctx, `
        SELECT EXISTS (
            SELECT 1 FROM information_schema.tables WHERE table_schema = 'public' AND table_name = $1
        )
    `, name)
	require.NoError(t, row.Scan(&exists))
	require.Truef(t, exists, "table %s does not exist", name)
}

func assertColumnExists(t *testing.T, ctx context.Context, db *sql.DB, table, column string) {
	t.Helper()
	var exists bool
	row := db.QueryRowContext(ctx, `
        SELECT EXISTS (
            SELECT 1 FROM information_schema.columns WHERE table_schema = 'public' AND table_name = $1 AND column_name = $2
        )
    `, table, column)
	require.NoError(t, row.Scan(&exists))
	require.Truef(t, exists, "column %s.%s does not exist", table, column)
}

func assertIndexExists(t *testing.T, ctx context.Context, db *sql.DB, name string) {
	t.Helper()
	var exists bool
	row := db.QueryRowContext(ctx, `
        SELECT EXISTS (
            SELECT 1 FROM pg_indexes WHERE schemaname = 'public' AND indexname = $1
        )
    `, name)
	require.NoError(t, row.Scan(&exists))
	require.Truef(t, exists, "index %s does not exist", name)
}

func assertColumnNotExists(t *testing.T, ctx context.Context, db *sql.DB, table, column string) {
	t.Helper()
	var exists bool
	row := db.QueryRowContext(ctx, `
        SELECT EXISTS (
            SELECT 1 FROM information_schema.columns WHERE table_schema = 'public' AND table_name = $1 AND column_name = $2
        )
    `, table, column)
	require.NoError(t, row.Scan(&exists))
	require.Falsef(t, exists, "column %s.%s still exists", table, column)
}

func assertIndexNotExists(t *testing.T, ctx context.Context, db *sql.DB, name string) {
	t.Helper()
	var exists bool
	row := db.QueryRowContext(ctx, `
        SELECT EXISTS (
            SELECT 1 FROM pg_indexes WHERE schemaname = 'public' AND indexname = $1
        )
    `, name)
	require.NoError(t, row.Scan(&exists))
	require.Falsef(t, exists, "index %s still exists", name)
}
