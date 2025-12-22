package preprocess

import (
	"fmt"

	"preprocess-service/internal/domain"
)

type jobError struct {
	Code      domain.ErrorCode
	Permanent bool
	Details   map[string]string
	err       error
}

func (e jobError) Error() string {
	return e.err.Error()
}

func (e jobError) Unwrap() error {
	return e.err
}

func temporaryError(code domain.ErrorCode, err error) error {
	return temporaryErrorWithDetails(code, err, nil)
}

func permanentError(code domain.ErrorCode, err error) error {
	return permanentErrorWithDetails(code, err, nil)
}

func temporaryErrorWithDetails(code domain.ErrorCode, err error, details map[string]string) error {
	return newJobError(code, false, err, details)
}

func permanentErrorWithDetails(code domain.ErrorCode, err error, details map[string]string) error {
	return newJobError(code, true, err, details)
}

func newJobError(code domain.ErrorCode, permanent bool, err error, details map[string]string) error {
	return jobError{
		Code:      code,
		Permanent: permanent,
		err:       err,
		Details:   cloneDetails(details),
	}
}

func (e jobError) String() string {
	return fmt.Sprintf("%s (permanent=%t): %v", e.Code, e.Permanent, e.err)
}

func (e jobError) DetailsMap() map[string]string {
	return cloneDetails(e.Details)
}

func cloneDetails(details map[string]string) map[string]string {
	if len(details) == 0 {
		return map[string]string{}
	}

	copied := make(map[string]string, len(details))
	for k, v := range details {
		copied[k] = v
	}

	return copied
}
