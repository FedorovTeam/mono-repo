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
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker binary not available for testcontainers")
	}

	ctx := context.Background()
	pgContainer, err := postgres.RunContainer(ctx,
		testcontainers.WithImage("postgres:16-alpine"),
		postgres.WithDatabase("documents"),
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

	assertTableExists(t, ctx, db, "documents")
	assertTableExists(t, ctx, db, "document_versions")
	assertTableExists(t, ctx, db, "document_status_logs")

	assertEnumValues(t, ctx, db)
	assertIndexes(t, ctx, db)
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

func assertEnumValues(t *testing.T, ctx context.Context, db *sql.DB) {
	rows, err := db.QueryContext(ctx, `
        SELECT enumlabel FROM pg_enum WHERE enumtypid = (SELECT oid FROM pg_type WHERE typname = 'document_status') ORDER BY enumsortorder
    `)
	require.NoError(t, err)
	defer rows.Close()

	var values []string
	for rows.Next() {
		var v string
		require.NoError(t, rows.Scan(&v))
		values = append(values, v)
	}
	require.Equal(t, []string{"Uploaded", "StoredOriginal", "ConvertedToMarkdown", "PublishedToGitHub", "Failed"}, values)
}

func assertIndexes(t *testing.T, ctx context.Context, db *sql.DB) {
	rows, err := db.QueryContext(ctx, `
        SELECT indexname FROM pg_indexes WHERE tablename = 'documents'
    `)
	require.NoError(t, err)
	defer rows.Close()

	indexes := map[string]bool{}
	for rows.Next() {
		var name string
		require.NoError(t, rows.Scan(&name))
		indexes[name] = true
	}
	require.True(t, indexes["idx_documents_markdown_key"])
	require.True(t, indexes["idx_documents_original_key"])
}
