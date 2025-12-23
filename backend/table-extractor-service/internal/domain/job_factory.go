package domain

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

var ErrInvalidJob = errors.New("invalid table extraction job")

type NewTableExtractionJobParams struct {
	ID uuid.UUID

	DocumentID string
	SourceURL  string
	LocalPath  string
	Original   ArtifactLocation

	ArtifactsBucket  string
	ArtifactsPrefix  string
	MarkdownLocation ArtifactLocation

	CorrelationID string
	CreatedAt     time.Time
}

// NewTableExtractionJob валидирует входные данные и возвращает агрегат в начальном состоянии.
func NewTableExtractionJob(params NewTableExtractionJobParams) (TableExtractionJob, error) {
	createdAt := params.CreatedAt
	if createdAt.IsZero() {
		createdAt = time.Now().UTC()
	}

	jobID := params.ID
	if jobID == uuid.Nil {
		jobID = uuid.New()
	}

	artifactsBucket := strings.TrimSpace(params.ArtifactsBucket)
	artifactsPrefix := strings.TrimSpace(params.ArtifactsPrefix)

	job := TableExtractionJob{
		ID:              jobID,
		DocumentID:      strings.TrimSpace(params.DocumentID),
		SourceURL:       strings.TrimSpace(params.SourceURL),
		LocalPath:       strings.TrimSpace(params.LocalPath),
		Original:        params.Original,
		ArtifactsBucket: artifactsBucket,
		ArtifactsPrefix: artifactsPrefix,
		MarkdownLocation: ArtifactLocation{
			Bucket:    strings.TrimSpace(params.MarkdownLocation.Bucket),
			ObjectKey: strings.TrimSpace(params.MarkdownLocation.ObjectKey),
		},
		Artifacts: BuildArtifactsWithMarkdown(
			artifactsBucket,
			artifactsPrefix,
			ArtifactLocation{
				Bucket:    strings.TrimSpace(params.MarkdownLocation.Bucket),
				ObjectKey: strings.TrimSpace(params.MarkdownLocation.ObjectKey),
			},
		),
		Status:        StatusPending,
		Attempts:      0,
		CorrelationID: strings.TrimSpace(params.CorrelationID),
		CreatedAt:     createdAt,
		UpdatedAt:     createdAt,
	}

	if err := job.Validate(); err != nil {
		return TableExtractionJob{}, err
	}

	return job, nil
}

// Validate проверяет инварианты доменной модели.
func (j TableExtractionJob) Validate() error {
	if j.ID == uuid.Nil {
		return fmt.Errorf("%w: id is required", ErrInvalidJob)
	}

	if strings.TrimSpace(j.DocumentID) == "" {
		return fmt.Errorf("%w: document_id is required", ErrInvalidJob)
	}
	if strings.TrimSpace(j.SourceURL) == "" && strings.TrimSpace(j.LocalPath) == "" {
		if strings.TrimSpace(j.Original.Bucket) == "" || strings.TrimSpace(j.Original.ObjectKey) == "" {
			return fmt.Errorf("%w: source is required (source_url/local_path or bucket+object)", ErrInvalidJob)
		}
	}
	if strings.TrimSpace(j.CorrelationID) == "" {
		return fmt.Errorf("%w: correlation_id is required", ErrInvalidJob)
	}
	if strings.TrimSpace(j.ArtifactsBucket) == "" {
		return fmt.Errorf("%w: artifacts_bucket is required", ErrInvalidJob)
	}
	if strings.TrimSpace(j.ArtifactsPrefix) == "" {
		return fmt.Errorf("%w: artifacts_prefix is required", ErrInvalidJob)
	}
	if (j.MarkdownLocation.Bucket == "") != (j.MarkdownLocation.ObjectKey == "") {
		return fmt.Errorf("%w: markdown_bucket and markdown_object_key must be set together", ErrInvalidJob)
	}
	if j.Artifacts == (TableArtifacts{}) {
		return fmt.Errorf("%w: artifacts must be initialized", ErrInvalidJob)
	}
	if j.CreatedAt.IsZero() || j.UpdatedAt.IsZero() {
		return fmt.Errorf("%w: timestamps must be set", ErrInvalidJob)
	}
	if j.Status == "" {
		return fmt.Errorf("%w: status must be set", ErrInvalidJob)
	}
	if (j.Status == StatusDone || j.Status == StatusFailed) && j.FinishedAt == nil {
		return fmt.Errorf("%w: finished_at is required for status %s", ErrInvalidJob, j.Status)
	}

	return nil
}
