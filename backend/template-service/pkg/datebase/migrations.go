package datebase

import (
	"database/sql"
	"fmt"
	"template-service/pkg/config"

	_ "github.com/lib/pq"
	"github.com/pressly/goose/v3"
)

// ApplyMigrationsGoose применяет миграции с помощью goose для Yugabyte/Postgres
func ApplyMigrationsGoose(cfg config.YugabyteConfig, migrationsPath string) error {
	dsn := fmt.Sprintf("host=%s port=%s user=%s password=%s dbname=%s sslmode=%s", cfg.Host, cfg.Port, cfg.User, cfg.Password, cfg.DB, cfg.SSLMode)
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		return fmt.Errorf("failed to open DB: %w", err)
	}
	defer db.Close()

	if err := goose.SetDialect("postgres"); err != nil {
		return fmt.Errorf("failed to set goose dialect: %w", err)
	}

	if err := goose.Up(db, migrationsPath); err != nil {
		return fmt.Errorf("goose up failed: %w", err)
	}
	return nil
}
