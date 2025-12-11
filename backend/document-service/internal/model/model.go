package model

import (
	"errors"
	"time"
)

type Status string

const (
	StatusUploaded            Status = "Uploaded"
	StatusStoredOriginal      Status = "StoredOriginal"
	StatusConvertedToMarkdown Status = "ConvertedToMarkdown"
	StatusPublishedToGitHub   Status = "PublishedToGitHub"
	StatusFailed              Status = "Failed"
)

var allowedTransitions = map[Status]Status{
	StatusUploaded:            StatusStoredOriginal,
	StatusStoredOriginal:      StatusConvertedToMarkdown,
	StatusConvertedToMarkdown: StatusPublishedToGitHub,
}

type JobStage string

const (
	JobStageStoreOriginal     JobStage = "store_original"
	JobStageConvertToMarkdown JobStage = "convert_to_markdown"
	JobStagePublishToGitHub   JobStage = "publish_to_github"
)

type JobStatus string

const (
	JobStatusPending    JobStatus = "pending"
	JobStatusInProgress JobStatus = "in_progress"
	JobStatusCompleted  JobStatus = "completed"
	JobStatusFailed     JobStatus = "failed"
)

func ValidateTransition(from, to Status) error {
	if to == StatusFailed {
		return nil
	}
	if from == to {
		return nil
	}

	if from == StatusFailed {
		switch to {
		case StatusStoredOriginal, StatusConvertedToMarkdown, StatusPublishedToGitHub:
			return nil
		default:
			return errors.New("invalid status transition")
		}
	}

	next, ok := allowedTransitions[from]
	if !ok {
		return errors.New("unknown current status")
	}
	if next != to {
		return errors.New("invalid status transition")
	}
	return nil
}

func ValidateStatus(val Status) error {
	switch val {
	case StatusUploaded, StatusStoredOriginal, StatusConvertedToMarkdown, StatusPublishedToGitHub, StatusFailed:
		return nil
	default:
		return errors.New("unsupported status")
	}
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
	Payload    map[string]any
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

type JobEvent struct {
	ID        string
	JobID     string
	Stage     JobStage
	Status    JobStatus
	EventType string
	Message   string
	Payload   map[string]any
	CreatedAt time.Time
}

func ValidateJobStage(stage JobStage) error {
	switch stage {
	case JobStageStoreOriginal, JobStageConvertToMarkdown, JobStagePublishToGitHub:
		return nil
	default:
		return errors.New("unsupported job stage")
	}
}

func ValidateJobStatus(status JobStatus) error {
	switch status {
	case JobStatusPending, JobStatusInProgress, JobStatusCompleted, JobStatusFailed:
		return nil
	default:
		return errors.New("unsupported job status")
	}
}
