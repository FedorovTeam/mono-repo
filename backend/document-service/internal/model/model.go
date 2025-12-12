package model

import (
	"time"

	"backend-document-service/internal/domain"
)

type Status = domain.DocumentStatus

const (
	StatusUploaded            Status = domain.DocumentStatusUploaded
	StatusStoredOriginal      Status = domain.DocumentStatusStoredOriginal
	StatusConvertedToMarkdown Status = domain.DocumentStatusConvertedToMarkdown
	StatusPublishedToGitHub   Status = domain.DocumentStatusPublishedToGitHub
	StatusFailed              Status = domain.DocumentStatusFailed
)

type JobStage = domain.JobStage

const (
	JobStageStoreOriginal     JobStage = domain.JobStageStoreOriginal
	JobStageConvertToMarkdown JobStage = domain.JobStageConvertToMarkdown
	JobStagePublishToGitHub   JobStage = domain.JobStagePublishToGitHub
)

type JobStatus = domain.JobStatus

const (
	JobStatusPending    JobStatus = domain.JobStatusPending
	JobStatusInProgress JobStatus = domain.JobStatusInProgress
	JobStatusCompleted  JobStatus = domain.JobStatusCompleted
	JobStatusFailed     JobStatus = domain.JobStatusFailed
)

func ValidateTransition(from, to Status) error {
	return domain.ValidateDocumentStatusTransition(domain.DocumentStatus(from), domain.DocumentStatus(to))
}

func ValidateStatus(val Status) error {
	return domain.ValidateDocumentStatus(domain.DocumentStatus(val))
}

type Document struct {
	ID                string
	Name              string
	Owner             string
	Repository        string
	Branch            string
	BaseBranch        string
	Path              string
	Status            Status
	ActualStatus      Status
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

type DocumentStatusLog struct {
	ID         string
	DocumentID string
	FromStatus Status
	ToStatus   Status
	WorkflowID string
	Reason     string
	CreatedAt  time.Time
}

type ProcessingJob struct {
	TaskID     string
	DocumentID string
	Stage      JobStage
	Status     JobStatus
	Attempts   int
	NextRunAt  time.Time
	// Payload хранит ссылки для внешних gRPC исполнителей (bucket/object_key, queue_id,
	// github_path/repository), а не бинарные данные документов.
	Payload   map[string]any
	CreatedAt time.Time
	UpdatedAt time.Time
}

type JobEvent struct {
	ID        string
	JobID     string
	Stage     JobStage
	Status    JobStatus
	EventType string
	Message   string
	// Payload события фиксирует входные/выходные ссылки шагов (например, object_key,
	// queue_id или commit_sha) без размещения контента.
	Payload   map[string]any
	CreatedAt time.Time
}

func ValidateJobStage(stage JobStage) error {
	return domain.ValidateJobStage(domain.JobStage(stage))
}

func ValidateJobStatus(status JobStatus) error {
	return domain.ValidateJobStatus(domain.JobStatus(status))
}
