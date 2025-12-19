package domain

import (
	"errors"
	"strings"
	"time"
)

// Document описывает знаниевый документ и связанную метаинформацию.
// Валидации и правила переходов статусов инкапсулированы в методах сущности.
type Document struct {
	ID                string
	Name              string
	Owner             string
	Repository        string
	Branch            string
	BaseBranch        string
	Path              string
	Status            DocumentStatus
	WorkflowID        string
	ActiveJobID       string
	ErrorReason       string
	OriginalBucket    string
	OriginalObjectKey string
	MarkdownBucket    string
	MarkdownObjectKey string
	CreatePR          bool
	CreatedAt         time.Time
	UpdatedAt         time.Time
	Versions          []DocumentVersion
}

// DocumentVersion хранит ссылки на конкретную версию объекта в хранилище.
type DocumentVersion struct {
	ID                string
	DocumentID        string
	Version           int32
	OriginalObjectKey string
	MarkdownObjectKey string
	MarkdownQueueID   string
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

// DocumentStatusLog фиксирует историю переходов документа.
type DocumentStatusLog struct {
	ID         string
	DocumentID string
	FromStatus DocumentStatus
	ToStatus   DocumentStatus
	WorkflowID string
	Reason     string
	CreatedAt  time.Time
}

// ValidateForCreation проверяет необходимые атрибуты нового документа.
func (d *Document) ValidateForCreation() error {
	switch {
	case strings.TrimSpace(d.ID) == "":
		return errors.New("id is required")
	case strings.TrimSpace(d.Name) == "":
		return errors.New("name is required")
	case strings.TrimSpace(d.Owner) == "":
		return errors.New("owner is required")
	case strings.TrimSpace(d.Repository) == "":
		return errors.New("repository is required")
	case strings.TrimSpace(d.Branch) == "":
		return errors.New("branch is required")
	case strings.TrimSpace(d.Path) == "":
		return errors.New("path is required")
	case strings.TrimSpace(d.OriginalBucket) == "":
		return errors.New("upload bucket is required")
	case strings.TrimSpace(d.MarkdownBucket) == "":
		return errors.New("markdown bucket is required")
	case strings.TrimSpace(d.OriginalObjectKey) == "":
		return errors.New("upload object key is required")
	case strings.TrimSpace(d.MarkdownObjectKey) == "":
		return errors.New("markdown object key is required")
	}

	if err := ValidateDocumentStatus(d.Status); err != nil {
		return err
	}

	return nil
}

// ApplyStatus проверяет и применяет новый статус, возвращая лог перехода.
func (d *Document) ApplyStatus(to DocumentStatus, reason, errorReason string, version *DocumentVersion) (DocumentStatusLog, error) {
	if err := ValidateDocumentStatusTransition(d.Status, to); err != nil {
		return DocumentStatusLog{}, err
	}

	if errorReason != "" {
		d.ErrorReason = errorReason
	}
	if version != nil {
		if version.OriginalObjectKey != "" {
			d.OriginalObjectKey = version.OriginalObjectKey
		}
		if version.MarkdownObjectKey != "" {
			d.MarkdownObjectKey = version.MarkdownObjectKey
		}
	}

	previousStatus := d.Status
	d.Status = to
	d.UpdatedAt = time.Now().UTC()
	return DocumentStatusLog{FromStatus: previousStatus, ToStatus: to, WorkflowID: d.WorkflowID, Reason: reason, CreatedAt: d.UpdatedAt}, nil
}
