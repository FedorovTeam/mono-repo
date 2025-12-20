package domain

import (
	preprocessv1 "preprocess-service/proto/backend/preprocess/v1"
)

// PreprocessStatus описывает доменные статусы задач предобработки.
// Значения совпадают со строковым представлением в БД, дополнительные методы обеспечивают
// маппинг во внешние контракты.
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

// ToProto конвертирует доменный статус в gRPC-контракт.
func (s PreprocessStatus) ToProto() preprocessv1.PreprocessStatus {
	switch s {
	case StatusProcessing:
		return preprocessv1.PreprocessStatus_PREPROCESS_STATUS_PROCESSING
	case StatusCompleted:
		return preprocessv1.PreprocessStatus_PREPROCESS_STATUS_COMPLETED
	case StatusFailed, StatusStopped:
		return preprocessv1.PreprocessStatus_PREPROCESS_STATUS_FAILED
	case StatusPending:
		return preprocessv1.PreprocessStatus_PREPROCESS_STATUS_PENDING
	default:
		return preprocessv1.PreprocessStatus_PREPROCESS_STATUS_UNSPECIFIED
	}
}

// StatusFromProto создает доменный статус из внешнего значения.
func StatusFromProto(status preprocessv1.PreprocessStatus) PreprocessStatus {
	switch status {
	case preprocessv1.PreprocessStatus_PREPROCESS_STATUS_PROCESSING:
		return StatusProcessing
	case preprocessv1.PreprocessStatus_PREPROCESS_STATUS_COMPLETED:
		return StatusCompleted
	case preprocessv1.PreprocessStatus_PREPROCESS_STATUS_FAILED:
		return StatusFailed
	case preprocessv1.PreprocessStatus_PREPROCESS_STATUS_PENDING:
		return StatusPending
	default:
		return StatusUnspecified
	}
}

// ToDB возвращает строковое представление для хранения в таблице preprocess_queue.
func (s PreprocessStatus) ToDB() string {
	switch s {
	case StatusProcessing:
		return "processing"
	case StatusCompleted:
		return "done"
	case StatusFailed:
		return "failed"
	case StatusStopped:
		return "stopped"
	case StatusPending:
		return "pending"
	case StatusUnspecified:
		return "pending"
	default:
		return "pending"
	}
}

// StatusFromDB конвертирует строковое значение из БД в доменный статус.
func StatusFromDB(value string) PreprocessStatus {
	switch value {
	case "pending":
		return StatusPending
	case "processing":
		return StatusProcessing
	case "done":
		return StatusCompleted
	case "failed":
		return StatusFailed
	case "stopped":
		return StatusStopped
	default:
		return StatusUnspecified
	}
}

func (s PreprocessStatus) IsTerminal() bool {
	return s == StatusCompleted || s == StatusFailed || s == StatusStopped
}
