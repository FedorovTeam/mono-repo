package preprocess

import (
	"preprocess-service/internal/domain"
	"preprocess-service/internal/usecase/ports"
)

const (
	StatusDone       = domain.StatusCompletedString
	StatusFailed     = domain.StatusFailedString
	StatusPending    = domain.StatusPendingString
	StatusProcessing = domain.StatusProcessingString
	StatusStopped    = domain.StatusStoppedString

	DefaultTargetMediaType = "application/vnd.openxmlformats-officedocument.wordprocessingml.document"
	callbackMaxAttempts    = 3
)

type Job = ports.Job

type FailureUpdate = ports.FailureUpdate

type callbackErrorInfo struct {
	Code    string
	Message string
}
