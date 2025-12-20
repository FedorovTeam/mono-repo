package database

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/mono-repo/table-extractor-service/internal/config"
	_ "modernc.org/sqlite"
)

type Dialect string

const (
	DialectPostgres Dialect = "postgres"
	DialectSQLite   Dialect = "sqlite"
)

func New(ctx context.Context, cfg config.DatabaseConfig) (*sql.DB, Dialect, error) {
	driver := strings.ToLower(cfg.Driver)
	var dialect Dialect

	switch driver {
	case "postgres", "postgresql", "pg", "pgx":
		dialect = DialectPostgres
		driver = "pgx"
	case "sqlite", "sqlite3":
		dialect = DialectSQLite
		driver = "sqlite"
	default:
		return nil, "", fmt.Errorf("unsupported database driver: %s", cfg.Driver)
	}

	db, err := sql.Open(driver, cfg.DSN)
	if err != nil {
		return nil, dialect, fmt.Errorf("open database: %w", err)
	}

	if cfg.MaxConnections > 0 {
		db.SetMaxOpenConns(cfg.MaxConnections)
		db.SetMaxIdleConns(cfg.MaxConnections)
	}

	timeout := cfg.ConnectionTimeout
	if timeout <= 0 {
		timeout = 5 * time.Second
	}

	pingCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	if err := db.PingContext(pingCtx); err != nil {
		_ = db.Close()
		return nil, dialect, fmt.Errorf("ping database: %w", err)
	}

	return db, dialect, nil
}
