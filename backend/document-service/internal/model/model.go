package model

import "backend-document-service/internal/domain"

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

type Document = domain.Document
type DocumentVersion = domain.DocumentVersion
type DocumentStatusLog = domain.DocumentStatusLog
type ProcessingJob = domain.ProcessingJob
type JobEvent = domain.JobEvent

func ValidateJobStage(stage JobStage) error {
	return domain.ValidateJobStage(domain.JobStage(stage))
}

func ValidateJobStatus(status JobStatus) error {
	return domain.ValidateJobStatus(domain.JobStatus(status))
}
