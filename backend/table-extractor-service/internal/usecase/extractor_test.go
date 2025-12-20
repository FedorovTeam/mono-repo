package usecase

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/mono-repo/table-extractor-service/internal/repository/memory"
	"github.com/mono-repo/table-extractor-service/internal/telemetry"
)

func TestExtractTablesIdempotencyByDocument(t *testing.T) {
	repo := memory.NewInMemoryRepository()
	uc := NewTableExtractorUseCase(repo, Config{
		QueueLimit:             10,
		DefaultArtifactsBucket: "artifacts",
		DefaultArtifactsPrefix: "tables",
	}, nil)

	ctx, correlationID := telemetry.EnsureCorrelationID(context.Background())
	job, err := uc.ExtractTablesFromPDF(ctx, ExtractTablesRequest{
		DocumentID:        "doc-1",
		OriginalBucket:    "bucket",
		OriginalObjectKey: "path/to.pdf",
		CorrelationID:     correlationID,
	})
	require.NoError(t, err)
	require.Equal(t, correlationID, job.CorrelationID)
	require.Equal(t, "doc-1", job.DocumentID)
	require.Equal(t, "artifacts", job.Artifacts.Markdown.Bucket)
	require.Equal(t, "tables/doc-1/"+job.ID.String()+"/tables.md", job.Artifacts.Markdown.ObjectKey)

	secondJob, err := uc.ExtractTablesFromPDF(context.Background(), ExtractTablesRequest{
		DocumentID:        "doc-1",
		OriginalBucket:    "bucket",
		OriginalObjectKey: "path/to.pdf",
		CorrelationID:     "another-correlation",
	})
	require.NoError(t, err)
	require.Equal(t, job.ID, secondJob.ID, "idempotent by document_id")
	require.Equal(t, job.CorrelationID, secondJob.CorrelationID, "keeps original correlation id")
}

func TestExtractTablesQueueLimit(t *testing.T) {
	repo := memory.NewInMemoryRepository()
	uc := NewTableExtractorUseCase(repo, Config{
		QueueLimit:             1,
		DefaultArtifactsBucket: "artifacts",
		DefaultArtifactsPrefix: "tables",
	}, nil)

	ctx, _ := telemetry.EnsureCorrelationID(context.Background())
	_, err := uc.ExtractTablesFromPDF(ctx, ExtractTablesRequest{
		DocumentID:      "doc-1",
		SourceURL:       "https://example.com/source.pdf",
		ArtifactsBucket: "artifacts",
		ArtifactsPrefix: "tables/doc-1",
		WaitForResult:   true,
		CorrelationID:   "",
	})
	require.NoError(t, err)

	_, err = uc.ExtractTablesFromPDF(context.Background(), ExtractTablesRequest{
		DocumentID:      "doc-2",
		SourceURL:       "https://example.com/source2.pdf",
		ArtifactsBucket: "artifacts",
	})
	require.ErrorIs(t, err, ErrQueueLimitReached)
}

func TestGetJobNotFound(t *testing.T) {
	repo := memory.NewInMemoryRepository()
	uc := NewTableExtractorUseCase(repo, Config{
		QueueLimit:             0,
		DefaultArtifactsBucket: "artifacts",
		DefaultArtifactsPrefix: "tables",
	}, nil)

	_, err := uc.GetJob(context.Background(), uuid.MustParse("00000000-0000-0000-0000-000000000000"))
	require.ErrorIs(t, err, ErrNotFound)
}
