// backend/docling-transformer-service/internal/backoff/backoff.go
package backoff

import (
	"errors"
	"time"
)

var (
	ErrInvalidInitialInterval     = errors.New("initial interval must be positive")
	ErrInvalidMaxInterval         = errors.New("max interval must be positive")
	ErrMaxIntervalLessThanInitial = errors.New("max interval cannot be less than initial interval")
	ErrInvalidAttempts            = errors.New("attempts must be positive")
	ErrInvalidFactor              = errors.New("backoff factor must be greater than 1")
)

// Policy описывает параметры экспоненциального backoff.
type Policy struct {
	InitialInterval time.Duration
	MaxInterval     time.Duration
	MaxAttempts     int
	Factor          float64
}

// Normalize возвращает копию политики с заполненными значениями по умолчанию.
func (p Policy) Normalize(defaults Policy) Policy {
	if p.InitialInterval <= 0 {
		p.InitialInterval = defaults.InitialInterval
	}
	if p.MaxInterval <= 0 {
		p.MaxInterval = defaults.MaxInterval
	}
	if p.MaxAttempts <= 0 {
		p.MaxAttempts = defaults.MaxAttempts
	}
	if p.Factor <= 1 {
		p.Factor = defaults.Factor
	}

	return p
}

// Validate проверяет, что интервалы и количество попыток имеют корректные значения.
func (p Policy) Validate() error {
	if p.InitialInterval <= 0 {
		return ErrInvalidInitialInterval
	}
	if p.MaxInterval <= 0 {
		return ErrInvalidMaxInterval
	}
	if p.MaxInterval < p.InitialInterval {
		return ErrMaxIntervalLessThanInitial
	}
	if p.MaxAttempts <= 0 {
		return ErrInvalidAttempts
	}
	if p.Factor <= 1 {
		return ErrInvalidFactor
	}

	return nil
}

// Strategy возвращает последовательность ожиданий на основе политики.
type Strategy struct {
	nextInterval time.Duration
	maxInterval  time.Duration
	factor       float64
}

// NewStrategy создаёт стратегию экспоненциального backoff.
func NewStrategy(policy Policy) Strategy {
	return Strategy{
		nextInterval: policy.InitialInterval,
		maxInterval:  policy.MaxInterval,
		factor:       policy.Factor,
	}
}

// NextInterval возвращает текущий интервал ожидания и рассчитывает следующий.
func (s *Strategy) NextInterval() time.Duration {
	interval := s.nextInterval

	next := time.Duration(float64(interval) * s.factor)
	if s.maxInterval > 0 && next > s.maxInterval {
		next = s.maxInterval
	}

	s.nextInterval = next

	return interval
}
