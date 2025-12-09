package migrations

import (
	"context"
	"database/sql"
	"embed"
	"log"

	"github.com/pressly/goose/v3"
)

//go:embed *.sql
var embedMigrations embed.FS

func Run(ctx context.Context, db *sql.DB) error {
	goose.SetBaseFS(embedMigrations)
	goose.SetTableName("schema_migrations_docling_transformer_service")

	if current, err := goose.GetDBVersion(db); err == nil {
		log.Printf("[migrations] current DB version: %d", current)
	} else {
		log.Printf("[migrations] cannot get current DB version: %v", err)
	}

	if err := goose.UpContext(ctx, db, "."); err != nil {
		return err
	}

	if current, err := goose.GetDBVersion(db); err == nil {
		log.Printf("[migrations] migrations applied, DB version: %d", current)
	} else {
		log.Printf("[migrations] migrations applied, but cannot get DB version: %v", err)
	}

	return nil
}
