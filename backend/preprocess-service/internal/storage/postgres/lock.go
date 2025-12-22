package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"preprocess-service/internal/domain"
)

// WorkerLocker реализует распределённый lock поверх Postgres advisory lock.
// Ключ фиксирован для очереди предобработки, чтобы одновременно работала одна реплика воркера.
type WorkerLocker struct {
	execer interface {
		Exec(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error)
		QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	}
}

func NewWorkerLocker(execer interface {
	Exec(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}) *WorkerLocker {
	return &WorkerLocker{execer: execer}
}

func (l *WorkerLocker) TryLock(ctx context.Context) (domain.LockHandle, bool, error) {
	const lockKey int64 = 8999773311

	var locked bool
	if err := l.execer.QueryRow(ctx, "SELECT pg_try_advisory_lock($1)", lockKey).Scan(&locked); err != nil {
		return nil, false, fmt.Errorf("acquire worker lock: %w", err)
	}

	if !locked {
		return nil, false, nil
	}

	releaser := func(ctx context.Context) error {
		var unlocked bool
		if err := l.execer.QueryRow(ctx, "SELECT pg_advisory_unlock($1)", lockKey).Scan(&unlocked); err != nil {
			return fmt.Errorf("release worker lock: %w", err)
		}
		if !unlocked {
			return fmt.Errorf("release worker lock: not held")
		}
		return nil
	}

	return advisoryLockHandle{releaser: releaser}, true, nil
}

type advisoryLockHandle struct {
	releaser func(ctx context.Context) error
}

func (h advisoryLockHandle) Release(ctx context.Context) error {
	if h.releaser == nil {
		return nil
	}
	return h.releaser(ctx)
}
