package migrations

import (
	"context"
	"database/sql"
	"embed"
	"fmt"

	"github.com/pressly/goose/v3"
)

//go:embed *.sql
var embeddedMigrations embed.FS

const migrationsTable = "schema_migrations_table_extractor_service"
const defaultDialect = "postgres"

func Up(ctx context.Context, db *sql.DB, dialect string) error {
	goose.SetBaseFS(embeddedMigrations)
	goose.SetTableName(migrationsTable)

	if dialect == "" {
		dialect = defaultDialect
	}

	if err := goose.SetDialect(dialect); err != nil {
		return fmt.Errorf("set goose dialect: %w", err)
	}

	if err := goose.UpContext(ctx, db, "."); err != nil {
		return fmt.Errorf("apply migrations: %w", err)
	}

	return nil
}
