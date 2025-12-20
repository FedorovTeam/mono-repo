package domain

import "time"

type ErrorCode string

const (
	ErrorCodeProcessingError ErrorCode = "processing_error"
)

// RetryPolicy описывает стратегию вычисления задержек и остановки задач.
type RetryPolicy struct {
	Backoff time.Duration
}

const DefaultBackoff = 30 * time.Second

func NewRetryPolicy(backoff time.Duration) RetryPolicy {
	if backoff <= 0 {
		backoff = DefaultBackoff
	}

	return RetryPolicy{Backoff: backoff}
}

// Delay возвращает задержку перед следующей попыткой в зависимости от номера попытки (>=1).
func (p RetryPolicy) Delay(attempt int) time.Duration {
	if attempt <= 0 {
		attempt = 1
	}

	return p.Backoff * time.Duration(attempt)
}

// NextRun вычисляет время следующего запуска с учетом номера попытки.
func (p RetryPolicy) NextRun(now time.Time, attempt int) time.Time {
	return now.Add(p.Delay(attempt)).UTC()
}

// ShouldStop определяет, нужно ли завершить задачу окончательно.
func (p RetryPolicy) ShouldStop(attempt, maxAttempts int, permanent bool) bool {
	if permanent {
		return true
	}

	if maxAttempts > 0 && attempt >= maxAttempts {
		return true
	}

	return false
}
