package preprocess

import "preprocess-service/internal/usecase/ports"

const (
	StatusDone       = "done"
	StatusFailed     = "failed"
	StatusPending    = "pending"
	StatusProcessing = "processing"
	StatusStopped    = "stopped"

	DefaultTargetMediaType = "application/vnd.openxmlformats-officedocument.wordprocessingml.document"
	callbackMaxAttempts    = 3
)

type Job = ports.Job

type FailureUpdate = ports.FailureUpdate

type callbackErrorInfo struct {
	Code    string
	Message string
}
