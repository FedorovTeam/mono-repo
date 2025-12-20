package preprocess

import (
	"preprocess-service/internal/domain"
)

const (
	StatusDone       domain.PreprocessStatus = domain.StatusCompleted
	StatusFailed     domain.PreprocessStatus = domain.StatusFailed
	StatusPending    domain.PreprocessStatus = domain.StatusPending
	StatusProcessing domain.PreprocessStatus = domain.StatusProcessing
	StatusStopped    domain.PreprocessStatus = domain.StatusStopped

	DefaultTargetMediaType = "application/vnd.openxmlformats-officedocument.wordprocessingml.document"
	callbackMaxAttempts    = 3
)

type Job = domain.PreprocessJob

type FailureUpdate = domain.FailureUpdate

type callbackErrorInfo struct {
	Code    domain.ErrorCode
	Message string
}
