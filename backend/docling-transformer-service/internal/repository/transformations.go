// backend/docling-transformer-service/internal/repository/transformations.go
package repository

import (
	"time"

	"github.com/google/uuid"
	"github.com/mono-repo/docling-transformer-service/internal/domain"
	"github.com/mono-repo/docling-transformer-service/internal/domain/transformations"
)

type TransformationDTO struct {
	ID               uuid.UUID
	Status           transformations.TransformationStatus
	RetryCount       int
	DoclingTaskID    string
	DoclingJobID     string
	DoclingPollCount int
	NextPollAt       *time.Time

	DocumentID        string
	OriginalBucket    string
	OriginalObjectKey string

	SourceBucket    string
	SourceObjectKey string
	SourceURL       string

	MarkdownBucket    string
	MarkdownObjectKey string
	FilenameExtension string

	CorrelationID string

	CreatedAt  time.Time
	StartedAt  *time.Time
	FinishedAt *time.Time
	Error      *string

	TableMetadata *domain.TableMetadata
}

func (dto TransformationDTO) ToDomain() *transformations.TransformationTask {
	return &transformations.TransformationTask{
		ID:               dto.ID,
		Status:           dto.Status,
		RetryCount:       dto.RetryCount,
		DoclingTaskID:    dto.DoclingTaskID,
		DoclingJobID:     dto.DoclingJobID,
		DoclingPollCount: dto.DoclingPollCount,
		NextPollAt:       dto.NextPollAt,

		DocumentID:        dto.DocumentID,
		OriginalBucket:    dto.OriginalBucket,
		OriginalObjectKey: dto.OriginalObjectKey,

		SourceBucket:    dto.SourceBucket,
		SourceObjectKey: dto.SourceObjectKey,
		SourceURL:       dto.SourceURL,

		MarkdownBucket:    dto.MarkdownBucket,
		MarkdownObjectKey: dto.MarkdownObjectKey,
		FilenameExtension: dto.FilenameExtension,

		CorrelationID: dto.CorrelationID,

		CreatedAt:  dto.CreatedAt,
		StartedAt:  dto.StartedAt,
		FinishedAt: dto.FinishedAt,
		Error:      dto.Error,

		TableMetadata: dto.TableMetadata,
	}
}
