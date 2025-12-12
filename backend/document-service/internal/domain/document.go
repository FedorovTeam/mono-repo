package domain

import "time"

// Document описывает знания, обрабатываемые сервисом.
type Document struct {
	ID                string
	Name              string
	Owner             string
	Repository        string
	Branch            string
	BaseBranch        string
	Path              string
	Status            DocumentStatus
	ActualStatus      DocumentStatus
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

// DocumentVersion фиксирует конкретную версию документа.
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

// DocumentStatusLog хранит историю смены статусов.
type DocumentStatusLog struct {
	ID         string
	DocumentID string
	FromStatus DocumentStatus
	ToStatus   DocumentStatus
	WorkflowID string
	Reason     string
	CreatedAt  time.Time
}
