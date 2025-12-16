package worker

import "fmt"

type jobError struct {
	Code      string
	Permanent bool
	err       error
}

func (e jobError) Error() string {
	return e.err.Error()
}

func (e jobError) Unwrap() error {
	return e.err
}

func temporaryError(code string, err error) error {
	return jobError{Code: code, Permanent: false, err: err}
}

func permanentError(code string, err error) error {
	return jobError{Code: code, Permanent: true, err: err}
}

func (e jobError) String() string {
	return fmt.Sprintf("%s (permanent=%t): %v", e.Code, e.Permanent, e.err)
}
