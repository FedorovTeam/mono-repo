// backend/docling-transformer-service/internal/domain/errors.go
package domain

// TemporaryError описывает ошибку, которую можно повторить.
type TemporaryError interface {
	error
	Temporary() bool
}

// StatusCoder позволяет пробрасывать HTTP-код для временных ошибок внешних сервисов.
type StatusCoder interface {
	StatusCode() int
}

// TemporarySubmitRetryExhaustedError сигнализирует, что попытки повторной отправки
// временно завершились неудачно и задачу нужно отложить до следующего запуска воркера.
type TemporarySubmitRetryExhaustedError struct {
	Err error
}

func (e *TemporarySubmitRetryExhaustedError) Error() string {
	if e == nil || e.Err == nil {
		return "temporary submit retry exhausted"
	}
	return e.Err.Error()
}

func (e *TemporarySubmitRetryExhaustedError) Unwrap() error { return e.Err }

func (e *TemporarySubmitRetryExhaustedError) Temporary() bool { return true }
