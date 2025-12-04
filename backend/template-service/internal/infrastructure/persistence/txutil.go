package persistence

import (
	"context"
	"database/sql"
)

// txKey нужен для передачи *sql.Tx через context
var TxKey = struct{}{}

// dbExecutor — интерфейс для *sql.DB и *sql.Tx
// (ExecContext, QueryContext, QueryRowContext)
type DbExecutor interface {
	ExecContext(ctx context.Context, query string, args ...interface{}) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...interface{}) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...interface{}) *sql.Row
}
