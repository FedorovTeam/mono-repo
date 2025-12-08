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

func ValidateTransition(from, to Status) error {
	if to == StatusFailed {
		return nil
	}
	if from == to {
		return nil
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
        WorkflowID        string
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
