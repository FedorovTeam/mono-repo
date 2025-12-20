package domain

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestNewTableExtractionJobInitializesArtifactsAndTimestamps(t *testing.T) {
	createdAt := time.Date(2024, 8, 10, 9, 0, 0, 0, time.UTC)
	jobID := uuid.MustParse("00000000-0000-0000-0000-000000000123")

	job, err := NewTableExtractionJob(NewTableExtractionJobParams{
		ID:              jobID,
		DocumentID:      "doc-123",
		Original:        ArtifactLocation{Bucket: "originals", ObjectKey: "doc-123.pdf"},
		ArtifactsBucket: "artifacts",
		ArtifactsPrefix: "tables/doc-123/" + jobID.String(),
		CorrelationID:   "corr-doc-123",
		CreatedAt:       createdAt,
	})
	require.NoError(t, err)

	require.Equal(t, jobID, job.ID)
	require.Equal(t, StatusPending, job.Status)
	require.Equal(t, 0, job.Attempts)
	require.Equal(t, createdAt, job.CreatedAt)
	require.Equal(t, createdAt, job.UpdatedAt)
	require.Equal(t, "tables/doc-123/"+jobID.String()+"/tables.md", job.Artifacts.Markdown.ObjectKey)
	require.Equal(t, "artifacts", job.Artifacts.Markdown.Bucket)
	require.Equal(t, "corr-doc-123", job.CorrelationID)
}

func TestNewTableExtractionJobValidatesRequiredFields(t *testing.T) {
	_, err := NewTableExtractionJob(NewTableExtractionJobParams{
		DocumentID:      "doc-123",
		Original:        ArtifactLocation{Bucket: "originals", ObjectKey: "doc-123.pdf"},
		ArtifactsBucket: "artifacts",
		ArtifactsPrefix: "tables/doc-123/abc",
		CorrelationID:   "",
		CreatedAt:       time.Now().UTC(),
	})

	require.ErrorIs(t, err, ErrInvalidJob)
}
