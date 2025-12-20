package domain

// PreprocessStatus описывает доменные статусы задач предобработки.
// Значения совпадают со строковым представлением в БД.
type PreprocessStatus string

const (
	StatusUnspecified PreprocessStatus = ""
	StatusPending     PreprocessStatus = "pending"
	StatusProcessing  PreprocessStatus = "processing"
	StatusCompleted   PreprocessStatus = "done"
	StatusFailed      PreprocessStatus = "failed"
	StatusStopped     PreprocessStatus = "stopped"
)

const (
	StatusPendingString    = string(StatusPending)
	StatusProcessingString = string(StatusProcessing)
	StatusCompletedString  = string(StatusCompleted)
	StatusFailedString     = string(StatusFailed)
	StatusStoppedString    = string(StatusStopped)
)

func (s PreprocessStatus) String() string {
	return string(s)
}

func (s PreprocessStatus) IsTerminal() bool {
	return s == StatusCompleted || s == StatusFailed || s == StatusStopped
}
