package document

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"backend-document-service/internal/domain"
)

type followerRepo struct {
	jobs          []domain.ProcessingJob
	createdJobs   []domain.ProcessingJob
	updatedStatus []domain.DocumentStatus
	document      *domain.Document
}

func (r *followerRepo) ListFinishedJobs(ctx context.Context, limit int, postpone time.Duration) ([]domain.ProcessingJob, error) {
	return r.jobs, nil
}

func (r *followerRepo) GetDocument(ctx context.Context, id string) (*domain.Document, error) {
	if r.document != nil {
		return r.document, nil
	}
	return &domain.Document{
		ID:                id,
		Status:            domain.DocumentStatusStoredOriginal,
		Owner:             "org",
		Repository:        "repo",
		Branch:            "main",
		BaseBranch:        "main",
		Path:              "docs/path.md",
		OriginalBucket:    "orig-bucket",
		OriginalObjectKey: "orig/key",
		MarkdownBucket:    "md-bucket",
		MarkdownObjectKey: "md/key",
		WorkflowID:        "corr-1",
		Versions:          []domain.DocumentVersion{{Version: 1, OriginalETag: "etag-default"}},
	}, nil
}

func (r *followerRepo) CreateProcessingJob(ctx context.Context, job *domain.ProcessingJob, event *domain.JobEvent) (*domain.ProcessingJob, error) {
	r.createdJobs = append(r.createdJobs, *job)
	return job, nil
}

func (r *followerRepo) UpdateStatus(ctx context.Context, id string, to domain.DocumentStatus, reason, errorReason string, version *domain.DocumentVersion) (*domain.Document, error) {
	r.updatedStatus = append(r.updatedStatus, to)
	return &domain.Document{ID: id, Status: to}, nil
}

func (r *followerRepo) UpdateProcessingJob(ctx context.Context, taskID string, status domain.JobStatus, nextRunAt *time.Time, incrementAttempt bool, payload map[string]any, event *domain.JobEvent) (*domain.ProcessingJob, error) {
	return &domain.ProcessingJob{TaskID: taskID, Status: status, Payload: payload}, nil
}

func TestJobFollowerSchedulesNextStages(t *testing.T) {
	repo := &followerRepo{
		jobs: []domain.ProcessingJob{
			{TaskID: "job-1", DocumentID: "doc-1", Stage: domain.JobStageStoreOriginal, Status: domain.JobStatusCompleted, Payload: domain.StoreOriginalPayload{OriginalETag: "etag-default"}.ToMap()},
			{TaskID: "job-2", DocumentID: "doc-1", Stage: domain.JobStageConvertToMarkdown, Status: domain.JobStatusCompleted},
		},
	}

	follower := NewJobFollower(repo, 10, time.Minute, PublishDefaults{
		CommitMessage: "commit message",
		PRTitle:       "PR title",
		PRBody:        "PR body",
		AuthorName:    "Author",
		AuthorEmail:   "author@example.com",
	}, domain.NewStageRouter(domain.RouterRules{}))

	err := follower.reconcile(context.Background())
	require.NoError(t, err)

	require.Len(t, repo.createdJobs, 2)

	require.Equal(t, domain.JobStageConvertToMarkdown, repo.createdJobs[0].Stage)
	convertPayload := domain.ConvertPayloadFromMap(repo.createdJobs[0].Payload)
	require.Equal(t, "orig-bucket", convertPayload.OriginalBucket)

	require.Equal(t, domain.JobStagePublishToGitHub, repo.createdJobs[1].Stage)
	publishPayload := domain.PublishPayloadFromMap(repo.createdJobs[1].Payload)
	require.Equal(t, "org/repo", publishPayload.Repository)
	require.Equal(t, "commit message", publishPayload.CommitMessage)
	require.Equal(t, "author@example.com", publishPayload.AuthorEmail)
}

func TestJobFollowerMarksFailed(t *testing.T) {
	repo := &followerRepo{
		jobs: []domain.ProcessingJob{{
			TaskID:     "job-err",
			DocumentID: "doc-1",
			Stage:      domain.JobStageConvertToMarkdown,
			Status:     domain.JobStatusFailed,
			Payload:    map[string]any{"last_error": "boom"},
		}},
	}

	follower := NewJobFollower(repo, 10, time.Minute, PublishDefaults{}, domain.NewStageRouter(domain.RouterRules{}))

	err := follower.reconcile(context.Background())
	require.NoError(t, err)

	require.Len(t, repo.updatedStatus, 1)
	require.Equal(t, domain.DocumentStatusFailed, repo.updatedStatus[0])
}

func TestJobFollowerSchedulesPreprocessWhenEnabled(t *testing.T) {
	router := domain.NewStageRouter(domain.RouterRules{PreprocessEnabled: true})
	repo := &followerRepo{
		jobs: []domain.ProcessingJob{
			{TaskID: "job-1", DocumentID: "doc-1", Stage: domain.JobStageStoreOriginal, Status: domain.JobStatusCompleted, Payload: domain.StoreOriginalPayload{OriginalETag: "etag-preprocess"}.ToMap()},
		},
		document: &domain.Document{
			ID:                "doc-1",
			Status:            domain.DocumentStatusStoredOriginal,
			OriginalBucket:    "orig-bucket",
			OriginalObjectKey: "incoming/file.doc",
			Versions:          []domain.DocumentVersion{{Version: 1, OriginalETag: "etag-preprocess"}},
		},
	}

	follower := NewJobFollower(repo, 5, time.Minute, PublishDefaults{}, router)

	err := follower.reconcile(context.Background())
	require.NoError(t, err)

	require.Len(t, repo.createdJobs, 1)
	require.Equal(t, domain.JobStagePreprocessToDocx, repo.createdJobs[0].Stage)
	payload := domain.PreprocessPayloadFromMap(repo.createdJobs[0].Payload)
	require.Equal(t, "incoming/file.doc", payload.OriginalObjectKey)
	require.Equal(t, "orig-bucket", payload.OriginalBucket)
	require.Equal(t, "etag-preprocess", payload.OriginalETag)
}

func TestJobFollowerSchedulesExtractTablesWhenEnabled(t *testing.T) {
	router := domain.NewStageRouter(domain.RouterRules{TableExtractorEnabled: true})
	repo := &followerRepo{
		jobs: []domain.ProcessingJob{
			{TaskID: "job-1", DocumentID: "doc-1", Stage: domain.JobStageStoreOriginal, Status: domain.JobStatusCompleted, Payload: domain.StoreOriginalPayload{OriginalETag: "etag-preprocess"}.ToMap()},
		},
		document: &domain.Document{
			ID:                "doc-1",
			Status:            domain.DocumentStatusStoredOriginal,
			OriginalBucket:    "orig-bucket",
			OriginalObjectKey: "incoming/file.pdf",
			MarkdownBucket:    "md-bucket",
			MarkdownObjectKey: "markdown/key.md",
			ExtractTables:     true,
			Versions:          []domain.DocumentVersion{{Version: 1, OriginalETag: "etag-preprocess"}},
		},
	}

	follower := NewJobFollower(repo, 5, time.Minute, PublishDefaults{}, router)

	err := follower.reconcile(context.Background())
	require.NoError(t, err)

	require.Len(t, repo.createdJobs, 1)
	require.Equal(t, domain.JobStageExtractTables, repo.createdJobs[0].Stage)
	payload := domain.ExtractTablesPayloadFromMap(repo.createdJobs[0].Payload)
	require.Equal(t, "orig-bucket", payload.OriginalBucket)
	require.Equal(t, "incoming/file.pdf", payload.OriginalObjectKey)
	require.Equal(t, "md-bucket", payload.ArtifactsBucket)
	require.Equal(t, tablesArtifactsPrefix("markdown/key.md"), payload.ArtifactsPrefix)
}

func TestJobFollowerSchedulesExtractTablesForTablesOnlyStrategy(t *testing.T) {
	router := domain.NewStageRouter(domain.RouterRules{TableExtractorEnabled: true})
	repo := &followerRepo{
		jobs: []domain.ProcessingJob{
			{TaskID: "job-1", DocumentID: "doc-1", Stage: domain.JobStageStoreOriginal, Status: domain.JobStatusCompleted, Payload: domain.StoreOriginalPayload{OriginalETag: "etag-preprocess"}.ToMap()},
		},
		document: &domain.Document{
			ID:                 "doc-1",
			Status:             domain.DocumentStatusStoredOriginal,
			OriginalBucket:     "orig-bucket",
			OriginalObjectKey:  "incoming/file.pdf",
			MarkdownBucket:     "md-bucket",
			MarkdownObjectKey:  "markdown/key.md",
			ConversionStrategy: domain.ConversionStrategyTablesOnly,
			Versions:           []domain.DocumentVersion{{Version: 1, OriginalETag: "etag-preprocess"}},
		},
	}

	follower := NewJobFollower(repo, 5, time.Minute, PublishDefaults{}, router)

	err := follower.reconcile(context.Background())
	require.NoError(t, err)

	require.Len(t, repo.createdJobs, 1)
	require.Equal(t, domain.JobStageExtractTables, repo.createdJobs[0].Stage)
}

func TestJobFollowerSchedulesExtractAfterConvertForDoclingStrategy(t *testing.T) {
	router := domain.NewStageRouter(domain.RouterRules{TableExtractorEnabled: true})
	repo := &followerRepo{
		jobs: []domain.ProcessingJob{
			{
				TaskID:     "job-convert",
				DocumentID: "doc-1",
				Stage:      domain.JobStageConvertToMarkdown,
				Status:     domain.JobStatusCompleted,
				Payload: (domain.ConvertPayload{
					MarkdownBucket:           "md-bucket",
					MarkdownObjectKey:        "markdown/key.md",
					DoclingMarkdownBucket:    "md-bucket",
					DoclingMarkdownObjectKey: "markdown/key.md",
					ConversionStrategy:       domain.ConversionStrategyDoclingWithTables,
				}).ToMap(),
			},
		},
		document: &domain.Document{
			ID:                 "doc-1",
			Status:             domain.DocumentStatusStoredOriginal,
			OriginalBucket:     "orig-bucket",
			OriginalObjectKey:  "incoming/file.pdf",
			MarkdownBucket:     "md-bucket",
			MarkdownObjectKey:  "markdown/key.md",
			ConversionStrategy: domain.ConversionStrategyDoclingWithTables,
		},
	}

	follower := NewJobFollower(repo, 5, time.Minute, PublishDefaults{}, router)

	err := follower.reconcile(context.Background())
	require.NoError(t, err)

	require.Len(t, repo.createdJobs, 1)
	require.Equal(t, domain.JobStageExtractTables, repo.createdJobs[0].Stage)
	payload := domain.ExtractTablesPayloadFromMap(repo.createdJobs[0].Payload)
	require.Equal(t, "markdown/key.md", payload.DoclingMarkdownObjectKey)
	require.Equal(t, "md-bucket", payload.DoclingMarkdownBucket)
	require.True(t, payload.ExtractTables)
}

func TestJobFollowerSchedulesExtractAfterDoclingTableDetection(t *testing.T) {
	router := domain.NewStageRouter(domain.RouterRules{TableExtractorEnabled: true, TableDetectionMinCount: 1})
	repo := &followerRepo{
		jobs: []domain.ProcessingJob{
			{
				TaskID:     "job-convert",
				DocumentID: "doc-1",
				Stage:      domain.JobStageConvertToMarkdown,
				Status:     domain.JobStatusCompleted,
				Payload: (domain.ConvertPayload{
					MarkdownBucket:           "md-bucket",
					MarkdownObjectKey:        "markdown/key.md",
					DoclingMarkdownBucket:    "docling-bucket",
					DoclingMarkdownObjectKey: "docling/key.md",
					ConversionStrategy:       domain.ConversionStrategyDoclingWithTables,
					TableCount:               2,
					TablePages:               []int32{1, 3},
				}).ToMap(),
			},
		},
		document: &domain.Document{
			ID:                 "doc-1",
			Status:             domain.DocumentStatusStoredOriginal,
			OriginalBucket:     "orig-bucket",
			OriginalObjectKey:  "incoming/file.docx",
			MarkdownBucket:     "md-bucket",
			MarkdownObjectKey:  "markdown/key.md",
			ConversionStrategy: domain.ConversionStrategyDoclingWithTables,
		},
	}

	follower := NewJobFollower(repo, 5, time.Minute, PublishDefaults{}, router)

	err := follower.reconcile(context.Background())
	require.NoError(t, err)

	require.Len(t, repo.createdJobs, 1)
	require.Equal(t, domain.JobStageExtractTables, repo.createdJobs[0].Stage)
	payload := domain.ExtractTablesPayloadFromMap(repo.createdJobs[0].Payload)
	require.Equal(t, "docling-bucket", payload.DoclingMarkdownBucket)
	require.Equal(t, "docling/key.md", payload.DoclingMarkdownObjectKey)
	require.Equal(t, "orig-bucket", payload.OriginalBucket)
	require.Equal(t, "incoming/file.docx", payload.OriginalObjectKey)
	require.True(t, payload.ExtractTables)
}

func TestPreprocessConversionForcesDocxExtension(t *testing.T) {
	repo := &followerRepo{
		jobs: []domain.ProcessingJob{
			{
				TaskID:     "job-preprocess",
				DocumentID: "doc-1",
				Stage:      domain.JobStagePreprocessToDocx,
				Status:     domain.JobStatusCompleted,
				Payload: (domain.PreprocessPayload{
					OriginalBucket:        "orig-bucket",
					OriginalObjectKey:     "incoming/file.doc",
					PreprocessedBucket:    "prep-bucket",
					PreprocessedObjectKey: "processed/file.docx",
					TargetObjectKey:       "processed/file.docx",
					FilenameExtension:     "doc",
				}).ToMap(),
			},
		},
		document: &domain.Document{
			ID:                "doc-1",
			Status:            domain.DocumentStatusStoredOriginal,
			OriginalBucket:    "orig-bucket",
			OriginalObjectKey: "incoming/file.doc",
		},
	}

	follower := NewJobFollower(repo, 5, time.Minute, PublishDefaults{}, domain.NewStageRouter(domain.RouterRules{}))

	err := follower.reconcile(context.Background())
	require.NoError(t, err)

	require.Len(t, repo.createdJobs, 1)
	require.Equal(t, domain.JobStageConvertToMarkdown, repo.createdJobs[0].Stage)
	payload := domain.ConvertPayloadFromMap(repo.createdJobs[0].Payload)
	require.Equal(t, "docx", payload.FilenameExtension)
	require.Equal(t, "processed/file.docx", payload.PreprocessedObjectKey)
	require.Equal(t, "prep-bucket", payload.PreprocessedBucket)
}

func TestScheduleNextUsesOriginalETag(t *testing.T) {
	doc := &domain.Document{
		ID:                "doc-id",
		Status:            domain.DocumentStatusStoredOriginal,
		OriginalBucket:    "bucket",
		OriginalObjectKey: "file.pdf",
		Versions:          []domain.DocumentVersion{{Version: 1, OriginalETag: "etag-1"}},
	}
	repo := &followerRepo{document: doc}
	follower := NewJobFollower(repo, 1, time.Minute, PublishDefaults{}, domain.NewStageRouter(domain.RouterRules{PreprocessEnabled: true}))
	job := domain.ProcessingJob{
		DocumentID: doc.ID,
		Stage:      domain.JobStageStoreOriginal,
		Status:     domain.JobStatusCompleted,
		Payload:    domain.StoreOriginalPayload{OriginalETag: "etag-from-job"}.ToMap(),
	}

	err := follower.scheduleNext(context.Background(), job, doc)

	require.NoError(t, err)
	require.Len(t, repo.createdJobs, 1)
	payload := domain.PreprocessPayloadFromMap(repo.createdJobs[0].Payload)
	require.Equal(t, "etag-1", payload.OriginalETag)
}

func TestScheduleNextFailsWithoutOriginalETag(t *testing.T) {
	doc := &domain.Document{
		ID:                "doc-id",
		Status:            domain.DocumentStatusStoredOriginal,
		OriginalBucket:    "bucket",
		OriginalObjectKey: "file.pdf",
	}
	repo := &followerRepo{document: doc}
	follower := NewJobFollower(repo, 1, time.Minute, PublishDefaults{}, domain.NewStageRouter(domain.RouterRules{PreprocessEnabled: true}))
	job := domain.ProcessingJob{
		DocumentID: doc.ID,
		Stage:      domain.JobStageStoreOriginal,
		Status:     domain.JobStatusCompleted,
		Payload:    map[string]any{},
	}

	err := follower.scheduleNext(context.Background(), job, doc)

	require.Error(t, err)
	require.Contains(t, err.Error(), "etag")
	require.Empty(t, repo.createdJobs)
}

func TestScheduleNextFallsBackToJobPayloadETag(t *testing.T) {
	doc := &domain.Document{
		ID:                "doc-id",
		Status:            domain.DocumentStatusStoredOriginal,
		OriginalBucket:    "bucket",
		OriginalObjectKey: "file.pdf",
	}
	repo := &followerRepo{document: doc}
	follower := NewJobFollower(repo, 1, time.Minute, PublishDefaults{}, domain.NewStageRouter(domain.RouterRules{PreprocessEnabled: true}))
	job := domain.ProcessingJob{
		DocumentID: doc.ID,
		Stage:      domain.JobStageStoreOriginal,
		Status:     domain.JobStatusCompleted,
		Payload:    domain.StoreOriginalPayload{OriginalETag: "etag-from-job"}.ToMap(),
	}

	err := follower.scheduleNext(context.Background(), job, doc)

	require.NoError(t, err)
	require.Len(t, repo.createdJobs, 1)
	payload := domain.PreprocessPayloadFromMap(repo.createdJobs[0].Payload)
	require.Equal(t, "etag-from-job", payload.OriginalETag)
}
