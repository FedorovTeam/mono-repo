package migrations

import (
	"context"
	"database/sql"
	"embed"

	"github.com/pressly/goose/v3"
)

//go:embed *.sql
var embedMigrations embed.FS

func Run(ctx context.Context, db *sql.DB) error {
	goose.SetBaseFS(embedMigrations)
	goose.SetTableName("schema_migrations_document_service")
	return goose.UpContext(ctx, db, ".")
}
