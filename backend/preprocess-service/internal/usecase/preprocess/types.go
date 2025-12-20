package preprocess

import "time"

const (
	StatusDone       = "done"
	StatusFailed     = "failed"
	StatusPending    = "pending"
	StatusProcessing = "processing"
	StatusStopped    = "stopped"

	DefaultTargetMediaType = "application/vnd.openxmlformats-officedocument.wordprocessingml.document"
	callbackMaxAttempts    = 3
)

type Job struct {
	JobID         string
	SourceBucket  string
	SourceKey     string
	SourceETag    string
	TargetBucket  string
	TargetKey     string
	CallbackURL   string
	CorrelationID string
	Attempts      int
	MaxAttempts   int
}

type FailureUpdate struct {
	Status       string
	NextRunAt    time.Time
	Code         string
	Message      string
	Permanent    bool
	TargetBucket string
	TargetKey    string
	Details      map[string]any
}

type callbackErrorInfo struct {
	Code    string
	Message string
}
