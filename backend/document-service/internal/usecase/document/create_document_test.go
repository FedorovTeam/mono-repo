package document

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"backend-document-service/internal/domain"
)

type repositoryStub struct {
	createdDoc *domain.Document
}

func (r *repositoryStub) CreateDocument(_ context.Context, doc *domain.Document, _ *domain.DocumentVersion, _ *domain.DocumentStatusLog) (*domain.Document, error) {
	r.createdDoc = doc
	return doc, nil
}

func (*repositoryStub) GetDocument(context.Context, string) (*domain.Document, error) {
	return nil, errors.New("not implemented")
}
func (*repositoryStub) ListDocuments(context.Context, int, int, *domain.DocumentStatus) ([]domain.Document, error) {
	return nil, errors.New("not implemented")
}
func (*repositoryStub) UpdateStatus(context.Context, string, domain.DocumentStatus, string, string, *domain.DocumentVersion) (*domain.Document, error) {
	return nil, errors.New("not implemented")
}
func (*repositoryStub) LastNonFailedStatus(context.Context, string) (domain.DocumentStatus, error) {
	return "", errors.New("not implemented")
}
func (*repositoryStub) CountStatusLogsWithReasonPrefix(context.Context, string, domain.DocumentStatus, string) (int, error) {
	return 0, errors.New("not implemented")
}
func (*repositoryStub) CreateProcessingJob(context.Context, *domain.ProcessingJob, *domain.JobEvent) (*domain.ProcessingJob, error) {
	return nil, errors.New("not implemented")
}
func (*repositoryStub) ActiveJob(context.Context, string) (*domain.ProcessingJob, error) {
	return nil, errors.New("not implemented")
}
func (*repositoryStub) AcquirePendingJobs(context.Context, domain.JobStage, int) ([]domain.ProcessingJob, error) {
	return nil, errors.New("not implemented")
}
func (*repositoryStub) RequeueStaleProcessingJobs(context.Context, time.Time, int, time.Time, string) ([]domain.ProcessingJob, error) {
	return nil, errors.New("not implemented")
}
func (*repositoryStub) UpdateProcessingJob(context.Context, string, domain.JobStatus, *time.Time, bool, map[string]any, *domain.JobEvent) (*domain.ProcessingJob, error) {
	return nil, errors.New("not implemented")
}

func TestCreateDocumentGeneratesDefaultPath(t *testing.T) {
	repo := &repositoryStub{}
	defaults := Defaults{
		OriginalBucket: "original-bucket",
		MarkdownBucket: "markdown-bucket",
		Owner:          "owner",
		Repository:     "repo",
		Branch:         "main",
		BaseBranch:     "main",
	}

	uc := NewCreateDocument(repo, defaults)
	doc, err := uc.Execute(context.Background(), CreateDocumentInput{
		FileName:          "file.docx",
		UploadBucket:      "original-bucket",
		UploadObjectKey:   "object-key",
		MarkdownBucket:    "markdown-bucket",
		MarkdownObjectKey: "markdown-key",
	})

	require.NoError(t, err)
	require.NotNil(t, doc)
	require.NotNil(t, repo.createdDoc)
	require.NotEmpty(t, repo.createdDoc.ID)
	require.True(t, strings.HasPrefix(repo.createdDoc.Path, "knowledge/docs/"))
	require.True(t, strings.HasSuffix(repo.createdDoc.Path, ".md"))
	require.Equal(t, fmt.Sprintf("knowledge/docs/%s.md", repo.createdDoc.ID), repo.createdDoc.Path)
}
