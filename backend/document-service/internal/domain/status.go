package domain

import "errors"

// DocumentStatus описывает этап жизненного цикла документа.
type DocumentStatus string

const (
	DocumentStatusUploaded            DocumentStatus = "Uploaded"
	DocumentStatusStoredOriginal      DocumentStatus = "StoredOriginal"
	DocumentStatusConvertedToMarkdown DocumentStatus = "ConvertedToMarkdown"
	DocumentStatusPublishedToGitHub   DocumentStatus = "PublishedToGitHub"
	DocumentStatusFailed              DocumentStatus = "Failed"
)

var allowedDocumentTransitions = map[DocumentStatus]DocumentStatus{
	DocumentStatusUploaded:            DocumentStatusStoredOriginal,
	DocumentStatusStoredOriginal:      DocumentStatusConvertedToMarkdown,
	DocumentStatusConvertedToMarkdown: DocumentStatusPublishedToGitHub,
}

// DocumentTransition фиксирует ожидаемое изменение статуса документа.
type DocumentTransition struct {
	From DocumentStatus
	To   DocumentStatus
}

// NewDocumentTransition валидирует и создаёт переход статуса.
func NewDocumentTransition(from, to DocumentStatus) (DocumentTransition, error) {
	if err := ValidateDocumentStatus(to); err != nil {
		return DocumentTransition{}, err
	}
	if err := ValidateDocumentStatusTransition(from, to); err != nil {
		return DocumentTransition{}, err
	}
	return DocumentTransition{From: from, To: to}, nil
}

// ValidateDocumentStatus проверяет, что значение известно домену.
func ValidateDocumentStatus(val DocumentStatus) error {
	switch val {
	case DocumentStatusUploaded, DocumentStatusStoredOriginal, DocumentStatusConvertedToMarkdown, DocumentStatusPublishedToGitHub, DocumentStatusFailed:
		return nil
	default:
		return errors.New("unsupported status")
	}
}

// ValidateDocumentStatusTransition проверяет допустимость перехода.
func ValidateDocumentStatusTransition(from, to DocumentStatus) error {
	if err := ValidateDocumentStatus(to); err != nil {
		return err
	}
	if to == DocumentStatusFailed || from == to {
		return nil
	}
	if from == DocumentStatusFailed {
		switch to {
		case DocumentStatusStoredOriginal, DocumentStatusConvertedToMarkdown, DocumentStatusPublishedToGitHub:
			return nil
		default:
			return errors.New("invalid status transition")
		}
	}
	next, ok := allowedDocumentTransitions[from]
	if !ok {
		return errors.New("unknown current status")
	}
	if next != to {
		return errors.New("invalid status transition")
	}
	return nil
}

// JobStage описывает этап фоновой задачи.
type JobStage string

const (
	JobStageStoreOriginal     JobStage = "store_original"
	JobStageConvertToMarkdown JobStage = "convert_to_markdown"
	JobStagePublishToGitHub   JobStage = "publish_to_github"
)

// JobStatus описывает состояние фоновой задачи.
type JobStatus string

const (
	JobStatusPending    JobStatus = "pending"
	JobStatusInProgress JobStatus = "in_progress"
	JobStatusCompleted  JobStatus = "completed"
	JobStatusFailed     JobStatus = "failed"
)

// ValidateJobStage проверяет известность стадии.
func ValidateJobStage(stage JobStage) error {
	switch stage {
	case JobStageStoreOriginal, JobStageConvertToMarkdown, JobStagePublishToGitHub:
		return nil
	default:
		return errors.New("unsupported job stage")
	}
}

// ValidateJobStatus проверяет известность статуса задачи.
func ValidateJobStatus(status JobStatus) error {
	switch status {
	case JobStatusPending, JobStatusInProgress, JobStatusCompleted, JobStatusFailed:
		return nil
	default:
		return errors.New("unsupported job status")
	}
}
