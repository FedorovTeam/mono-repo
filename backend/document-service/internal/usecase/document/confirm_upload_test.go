package document

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"backend-document-service/internal/domain"
)

type confirmUploadRepositoryStub struct {
	doc        *domain.Document
	jobCreated bool
}

func (r *confirmUploadRepositoryStub) CreateDocument(context.Context, *domain.Document, *domain.DocumentVersion, *domain.DocumentStatusLog) (*domain.Document, error) {
	return nil, errors.New("not implemented")
}

func (r *confirmUploadRepositoryStub) GetDocument(context.Context, string) (*domain.Document, error) {
	return r.doc, nil
}

func (r *confirmUploadRepositoryStub) ListDocuments(context.Context, int, int, *domain.DocumentStatus, *string) (*domain.DocumentList, error) {
	return nil, errors.New("not implemented")
}

func (r *confirmUploadRepositoryStub) UpdateStatus(context.Context, string, domain.DocumentStatus, string, string, *domain.DocumentVersion) (*domain.Document, error) {
	return nil, errors.New("not implemented")
}

func (r *confirmUploadRepositoryStub) UpdateDocumentOptions(_ context.Context, _ string, strategy domain.ConversionStrategy, extractTables *bool) (*domain.Document, error) {
	if r.doc == nil {
		return nil, errors.New("document not found")
	}
	if strategy != "" {
		r.doc.ConversionStrategy = strategy
	}
	if extractTables != nil {
		r.doc.ExtractTables = *extractTables
	}
	return r.doc, nil
}

func (r *confirmUploadRepositoryStub) LastNonFailedStatus(context.Context, string) (domain.DocumentStatus, error) {
	return "", errors.New("not implemented")
}

func (r *confirmUploadRepositoryStub) CountStatusLogsWithReasonPrefix(context.Context, string, domain.DocumentStatus, string) (int, error) {
	return 0, errors.New("not implemented")
}

func (r *confirmUploadRepositoryStub) CreateProcessingJob(_ context.Context, job *domain.ProcessingJob, _ *domain.JobEvent) (*domain.ProcessingJob, error) {
	r.jobCreated = true
	return job, nil
}

func (r *confirmUploadRepositoryStub) ActiveJob(context.Context, string) (*domain.ProcessingJob, error) {
	return nil, errors.New("not implemented")
}

func (r *confirmUploadRepositoryStub) AcquirePendingJobs(context.Context, domain.JobStage, int) ([]domain.ProcessingJob, error) {
	return nil, errors.New("not implemented")
}

func (r *confirmUploadRepositoryStub) RequeueStaleProcessingJobs(context.Context, time.Time, int, time.Time, string) ([]domain.ProcessingJob, error) {
	return nil, errors.New("not implemented")
}

func (r *confirmUploadRepositoryStub) UpdateProcessingJob(context.Context, string, domain.JobStatus, *time.Time, bool, map[string]any, *domain.JobEvent) (*domain.ProcessingJob, error) {
	return nil, errors.New("not implemented")
}

type storageStub struct {
	etag string
	err  error
}

func (s *storageStub) ValidateObject(context.Context, string, string) (string, error) {
	return s.etag, s.err
}

func TestConfirmUploadFailsWhenETagIsEmpty(t *testing.T) {
	repo := &confirmUploadRepositoryStub{
		doc: &domain.Document{
			ID:                "00000000-0000-0000-0000-000000000001",
			OriginalBucket:    "bucket",
			OriginalObjectKey: "object",
		},
	}
	uc := NewConfirmUpload(repo, &storageStub{}, nil)

	_, err := uc.Execute(context.Background(), ConfirmUploadInput{
		DocumentID: repo.doc.ID,
		Bucket:     repo.doc.OriginalBucket,
		ObjectKey:  repo.doc.OriginalObjectKey,
	})

	require.Error(t, err)
	require.Contains(t, err.Error(), "etag")
	require.False(t, repo.jobCreated)
}

func TestConfirmUploadAppliesRouterDefaults(t *testing.T) {
	repo := &confirmUploadRepositoryStub{
		doc: &domain.Document{
			ID:                 "00000000-0000-0000-0000-000000000002",
			OriginalBucket:     "bucket",
			OriginalObjectKey:  "incoming/report.pdf",
			MarkdownBucket:     "md",
			Path:               "reports/report.md",
			ConversionStrategy: domain.ConversionStrategyTablesOnly,
		},
	}
	router := domain.NewStageRouter(domain.RouterRules{
		TableExtractorEnabled:    true,
		TableExtractorExtensions: []string{"pdf"},
		ArtifactsBucket:          "tables-bucket",
	})
	uc := NewConfirmUpload(repo, &storageStub{etag: "etag"}, router)

	doc, err := uc.Execute(context.Background(), ConfirmUploadInput{
		DocumentID: repo.doc.ID,
		Bucket:     repo.doc.OriginalBucket,
		ObjectKey:  repo.doc.OriginalObjectKey,
	})

	require.NoError(t, err)
	require.True(t, doc.ExtractTables)
}
