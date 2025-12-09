package document

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/client"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"backend-document-service/internal/model"
	"backend-document-service/internal/storage"
)

type repoMock struct {
	doc         *model.Document
	err         error
	savedStatus model.Status
	workflowID  string
	savedError  string
}

func (r *repoMock) CreateDocument(ctx context.Context, doc *model.Document, version *model.DocumentVersion, log *model.DocumentStatusLog) (*model.Document, error) {
	if r.err != nil {
		return nil, r.err
	}
	if doc.ID == "" {
		doc.ID = uuid.NewString()
	}
	doc.CreatedAt = time.Now()
	doc.UpdatedAt = doc.CreatedAt
	doc.Versions = []model.DocumentVersion{*version}
	r.doc = doc
	return doc, nil
}

func (r *repoMock) GetDocument(ctx context.Context, id string) (*model.Document, error) {
	return r.doc, r.err
}
func (r *repoMock) ListDocuments(ctx context.Context, limit, offset int, status *model.Status) ([]model.Document, error) {
	if r.doc == nil {
		return nil, nil
	}
	return []model.Document{*r.doc}, nil
}
func (r *repoMock) UpdateStatus(ctx context.Context, id string, to model.Status, reason, errorReason string, version *model.DocumentVersion) (*model.Document, error) {
	if r.err != nil {
		return nil, r.err
	}
	if r.doc == nil {
		return nil, errors.New("missing doc")
	}
	r.savedStatus = to
	r.savedError = errorReason
	r.doc.Status = to
	if errorReason != "" {
		r.doc.ErrorReason = errorReason
	}
	if version != nil {
		if len(r.doc.Versions) == 0 {
			r.doc.Versions = []model.DocumentVersion{*version}
		} else {
			r.doc.Versions[len(r.doc.Versions)-1] = *version
		}
	}
	return r.doc, nil
}

func (r *repoMock) UpdateWorkflowID(ctx context.Context, id, workflowID string) (*model.Document, error) {
	if r.doc == nil {
		return nil, errors.New("missing doc")
	}
	r.workflowID = workflowID
	r.doc.WorkflowID = workflowID
	return r.doc, nil
}

type keyMock struct {
	lastData storage.KeyGenerationData
}

func (k *keyMock) GenerateKeys(ctx context.Context, data storage.KeyGenerationData) (storage.KeyPair, error) {
	k.lastData = data
	entityPath := strings.Trim(data.EntitySpecificPath, "/")
	if entityPath == "" {
		entityPath = "key"
	}
	base := entityPath
	return storage.KeyPair{OriginalObjectKey: base + "/original", MarkdownObjectKey: base}, nil
}

type failingKey struct{}

func (failingKey) GenerateKeys(ctx context.Context, data storage.KeyGenerationData) (storage.KeyPair, error) {
	return storage.KeyPair{}, errors.New("failed")
}

func TestCreateDocumentValidation(t *testing.T) {
	svc := New(&repoMock{}, &keyMock{}, nil, "", nil, "", "", "", "", "", "")
	_, err := svc.CreateDocument(context.Background(), CreateInput{})
	require.Error(t, err)

	_, err = svc.CreateDocument(context.Background(), CreateInput{FileName: "a"})
	require.Error(t, err)

	_, err = svc.CreateDocument(context.Background(), CreateInput{FileName: "a", ID: "bad-uuid"})
	require.Error(t, err)

	_, err = svc.CreateDocument(context.Background(), CreateInput{FileName: "a", Repository: "owner/repo/extra"})
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	require.Equal(t, codes.InvalidArgument, st.Code())
}

func TestCreateDocumentSuccess(t *testing.T) {
	repo := &repoMock{}
	keys := &keyMock{}
	svc := New(repo, keys, nil, "", nil, "orig", "md", "owner", "repo", "main", "main")
	doc, err := svc.CreateDocument(context.Background(), CreateInput{FileName: "name", FilenameExt: "docx", Repository: "owner/repo", Branch: "main", Path: "docs/name.md", PublishPath: "docs/publish/name.md", EntitySpecificPath: "docs/articles/name", BaseBranch: "main"})
	require.NoError(t, err)
	require.NotEmpty(t, doc.ID)
	require.Equal(t, doc.ID, doc.WorkflowID)
	require.Equal(t, "repo", doc.Repository)
	require.Equal(t, "main", doc.Branch)
	require.Equal(t, "docs/name.md", doc.Path)
	require.Equal(t, "orig", doc.OriginalBucket)
	require.Equal(t, "md", doc.MarkdownBucket)
	require.Contains(t, doc.OriginalObjectKey, "docs/articles/name")
	require.Contains(t, doc.MarkdownObjectKey, "docs/articles/name")
	require.Equal(t, model.StatusUploaded, doc.Status)
	require.Len(t, doc.Versions, 1)
	require.Equal(t, doc.ID, keys.lastData.DocumentID)
	require.Equal(t, "docs/name.md", keys.lastData.Path)
	require.Equal(t, "docs/publish/name.md", keys.lastData.PublishPath)
	require.Equal(t, "docs/articles/name", keys.lastData.EntitySpecificPath)
	require.Equal(t, "owner", doc.Owner)
}

func TestCreateDocumentSplitsOwnerFromRepository(t *testing.T) {
	repo := &repoMock{}
	keys := &keyMock{}
	svc := New(repo, keys, nil, "", nil, "orig", "md", "", "", "main", "main")

	doc, err := svc.CreateDocument(context.Background(), CreateInput{FileName: "name", Repository: "team/docs", Branch: "dev"})
	require.NoError(t, err)
	require.Equal(t, "team", doc.Owner)
	require.Equal(t, "docs", doc.Repository)
}

func TestCreateDocumentRejectsConflictingOwner(t *testing.T) {
	svc := New(&repoMock{}, &keyMock{}, nil, "", nil, "orig", "md", "default-owner", "repo", "main", "main")

	_, err := svc.CreateDocument(context.Background(), CreateInput{FileName: "name", Owner: "other", Repository: "owner/repo"})
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	require.Equal(t, codes.InvalidArgument, st.Code())
}

func TestCreateDocumentRespectsProvidedID(t *testing.T) {
	repo := &repoMock{}
	svc := New(repo, &keyMock{}, nil, "", nil, "orig", "md", "owner", "repo", "main", "main")
	existingID := uuid.NewString()

	doc, err := svc.CreateDocument(context.Background(), CreateInput{ID: existingID, FileName: "name", FilenameExt: "docx", Owner: "owner"})
	require.NoError(t, err)
	require.Equal(t, existingID, doc.ID)
	require.Equal(t, existingID, doc.WorkflowID)
}

type workflowStarterMock struct {
	called bool
}

func (w *workflowStarterMock) ExecuteWorkflow(ctx context.Context, options client.StartWorkflowOptions, workflow interface{}, args ...interface{}) (client.WorkflowRun, error) {
	w.called = true
	return nil, nil
}

func TestCreateDocumentDoesNotStartWorkflowUntilConfirmation(t *testing.T) {
	repo := &repoMock{}
	starter := &workflowStarterMock{}
	svc := New(repo, &keyMock{}, starter, "queue", nil, "orig", "md", "owner", "repo", "main", "main")

	_, err := svc.CreateDocument(context.Background(), CreateInput{FileName: "name.docx", FilenameExt: "docx"})
	require.NoError(t, err)
	require.False(t, starter.called)
}

type workflowRunMock struct{}

func (workflowRunMock) GetID() string    { return "doc-123" }
func (workflowRunMock) GetRunID() string { return "run-123" }
func (workflowRunMock) Get(ctx context.Context, valuePtr interface{}) error {
	return nil
}
func (workflowRunMock) GetWithOptions(ctx context.Context, valuePtr interface{}, options client.WorkflowRunGetOptions) error {
	return nil
}

type workflowStarterSuccess struct{}

func (workflowStarterSuccess) ExecuteWorkflow(ctx context.Context, options client.StartWorkflowOptions, workflow interface{}, args ...interface{}) (client.WorkflowRun, error) {
	return workflowRunMock{}, nil
}

func TestConfirmUploadStartsWorkflow(t *testing.T) {
	repo := &repoMock{doc: &model.Document{ID: uuid.NewString(), Name: "file.docx", Owner: "owner", Repository: "repo", Branch: "main", BaseBranch: "main", OriginalBucket: "bucket", OriginalObjectKey: "org/p.docx", Status: model.StatusUploaded, Versions: []model.DocumentVersion{{Version: 1, OriginalObjectKey: "org/p.docx"}}}}
	svc := New(repo, &keyMock{}, workflowStarterSuccess{}, "queue", nil, "orig", "md", "owner", "repo", "main", "main")

	doc, err := svc.ConfirmUpload(context.Background(), ConfirmUploadInput{DocumentID: repo.doc.ID, Bucket: "bucket", ObjectKey: "org/p.docx"})
	require.NoError(t, err)
	require.Equal(t, "doc-123", doc.WorkflowID)
	require.Equal(t, "doc-123", repo.workflowID)
}

func TestUpdateStatusValidation(t *testing.T) {
	svc := New(&repoMock{doc: &model.Document{ID: uuid.NewString()}}, &keyMock{}, nil, "", nil, "orig", "md", "owner", "repo", "main", "main")
	_, err := svc.UpdateStatus(context.Background(), UpdateStatusInput{DocumentID: "bad", Status: model.StatusFailed})
	require.Error(t, err)
}

func TestUpdateStatusDelegates(t *testing.T) {
	repo := &repoMock{doc: &model.Document{ID: uuid.NewString(), Status: model.StatusUploaded}}
	svc := New(repo, &keyMock{}, nil, "", nil, "orig", "md", "owner", "repo", "main", "main")
	doc, err := svc.UpdateStatus(context.Background(), UpdateStatusInput{DocumentID: repo.doc.ID, Status: model.StatusStoredOriginal})
	require.NoError(t, err)
	require.Equal(t, model.StatusStoredOriginal, doc.Status)
	require.Equal(t, model.StatusStoredOriginal, repo.savedStatus)
}

func TestUpdateStatusSavesErrorReason(t *testing.T) {
	repo := &repoMock{doc: &model.Document{ID: uuid.NewString(), Status: model.StatusUploaded}}
	svc := New(repo, &keyMock{}, nil, "", nil, "orig", "md", "owner", "repo", "main", "main")

	doc, err := svc.UpdateStatus(context.Background(), UpdateStatusInput{DocumentID: repo.doc.ID, Status: model.StatusFailed, Reason: "boom"})
	require.NoError(t, err)
	require.Equal(t, "boom", repo.savedError)
	require.Equal(t, "boom", doc.ErrorReason)
}

func TestCreateDocumentKeyFailure(t *testing.T) {
	svc := New(&repoMock{}, failingKey{}, nil, "", nil, "a", "b", "owner", "repo", "main", "main")
	_, err := svc.CreateDocument(context.Background(), CreateInput{FileName: "name"})
	require.Error(t, err)
}
