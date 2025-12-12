package persistence

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"backend-storage-service/internal/application"
)

// PgxPool описывает зависимость, необходимую для создания транзакций.
type PgxPool interface {
	BeginTx(ctx context.Context, txOptions pgx.TxOptions) (pgx.Tx, error)
}

// TxManager реализует application.TxManager поверх pgxpool.
type TxManager struct {
	pool PgxPool
}

// NewTxManager создаёт менеджер транзакций.
func NewTxManager(pool *pgxpool.Pool) *TxManager {
	return &TxManager{pool: pool}
}

// Begin начинает транзакцию с уровнем изоляции ReadCommitted.
func (m *TxManager) Begin(ctx context.Context) (application.Tx, error) {
	tx, err := m.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return nil, err
	}

	return &txWrapper{tx: tx}, nil
}

type txWrapper struct {
	tx pgx.Tx
}

func (t *txWrapper) Exec(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error) {
	return t.tx.Exec(ctx, sql, arguments...)
}

func (t *txWrapper) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	return t.tx.QueryRow(ctx, sql, args...)
}

func (t *txWrapper) Commit(ctx context.Context) error {
	return t.tx.Commit(ctx)
}

func (t *txWrapper) Rollback(ctx context.Context) error {
	return t.tx.Rollback(ctx)
}
