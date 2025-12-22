package domain

import "context"

// LockHandle представляет активный распределённый замок.
// Ресурс должен быть освобождён перед остановкой сервиса.
type LockHandle interface {
	Release(ctx context.Context) error
}

// Locker описывает механизм распределённой блокировки.
// Возвращает хендл и флаг захвата.
type Locker interface {
	TryLock(ctx context.Context) (LockHandle, bool, error)
}
