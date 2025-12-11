package document

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"backend-document-service/internal/model"
	"backend-document-service/internal/storage"
)

type repoMock struct {
	doc         *model.Document
	docs        []model.Document
	err         error
	savedStatus model.Status
	savedError  string
	attempts    int
	job         *model.ProcessingJob
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
	if len(r.docs) > 0 {
		if limit == 0 {
			limit = len(r.docs)
		}
		if offset >= len(r.docs) {
			return nil, nil
		}
		end := offset + limit
		if end > len(r.docs) {
			end = len(r.docs)
		}
		return r.docs[offset:end], nil
	}
	if r.doc == nil {
		return nil, nil
	}
	return []model.Document{*r.doc}, nil
}

func (r *repoMock) LastNonFailedStatus(ctx context.Context, id string) (model.Status, error) {
	if r.doc != nil {
		return r.doc.Status, nil
	}
	return "", nil
}
func (r *repoMock) CountStatusLogsWithReasonPrefix(ctx context.Context, id string, status model.Status, reasonPrefix string) (int, error) {
	return r.attempts, r.err
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

func (r *repoMock) CreateProcessingJob(ctx context.Context, job *model.ProcessingJob, event *model.JobEvent) (*model.ProcessingJob, error) {
	if r.err != nil {
		return nil, r.err
	}
	if job.TaskID == "" {
		job.TaskID = uuid.NewString()
	}
	r.job = job
	return job, nil
}

func (r *repoMock) UpdateProcessingJob(ctx context.Context, taskID string, status model.JobStatus, nextRunAt *time.Time, incrementAttempt bool, payload map[string]any, event *model.JobEvent) (*model.ProcessingJob, error) {
	if r.err != nil {
		return nil, r.err
	}
	return &model.ProcessingJob{TaskID: taskID, Status: status, Payload: payload}, nil
}

func (r *repoMock) ActiveJob(ctx context.Context, documentID string) (*model.ProcessingJob, error) {
	if r.job != nil && r.job.DocumentID == documentID {
		return r.job, nil
	}
	return nil, nil
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
	svc := New(&repoMock{}, &keyMock{}, nil, "", "", "", "", "", "")
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
	svc := New(repo, keys, nil, "orig", "md", "owner", "repo", "main", "main")
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
	svc := New(repo, keys, nil, "orig", "md", "", "", "main", "main")

	doc, err := svc.CreateDocument(context.Background(), CreateInput{FileName: "name", Repository: "team/docs", Branch: "dev"})
	require.NoError(t, err)
	require.Equal(t, "team", doc.Owner)
	require.Equal(t, "docs", doc.Repository)
}

func TestCreateDocumentRejectsConflictingOwner(t *testing.T) {
	svc := New(&repoMock{}, &keyMock{}, nil, "orig", "md", "default-owner", "repo", "main", "main")

	_, err := svc.CreateDocument(context.Background(), CreateInput{FileName: "name", Owner: "other", Repository: "owner/repo"})
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	require.Equal(t, codes.InvalidArgument, st.Code())
}

func TestCreateDocumentRespectsProvidedID(t *testing.T) {
	repo := &repoMock{}
	svc := New(repo, &keyMock{}, nil, "orig", "md", "owner", "repo", "main", "main")
	existingID := uuid.NewString()

	doc, err := svc.CreateDocument(context.Background(), CreateInput{ID: existingID, FileName: "name", FilenameExt: "docx", Owner: "owner"})
	require.NoError(t, err)
	require.Equal(t, existingID, doc.ID)
	require.Equal(t, existingID, doc.WorkflowID)
}

func TestConfirmUploadEnqueuesStoreOriginalJob(t *testing.T) {
	repo := &repoMock{doc: &model.Document{ID: uuid.NewString(), Name: "file.docx", Owner: "owner", Repository: "repo", Branch: "main", BaseBranch: "main", OriginalBucket: "bucket", OriginalObjectKey: "org/p.docx", Status: model.StatusUploaded, Versions: []model.DocumentVersion{{Version: 1, OriginalObjectKey: "org/p.docx"}}}}
	svc := New(repo, &keyMock{}, nil, "orig", "md", "owner", "repo", "main", "main")

	_, err := svc.ConfirmUpload(context.Background(), ConfirmUploadInput{DocumentID: repo.doc.ID, Bucket: "bucket", ObjectKey: "org/p.docx"})
	require.NoError(t, err)
	require.Equal(t, model.JobStageStoreOriginal, repo.job.Stage)
	require.Equal(t, model.JobStatusPending, repo.job.Status)
	require.Equal(t, repo.doc.ID, repo.job.DocumentID)
	require.Equal(t, "bucket", repo.job.Payload["bucket"])
	require.Equal(t, "org/p.docx", repo.job.Payload["object_key"])
}

func TestConfirmUploadIgnoresAlreadyStartedWorkflow(t *testing.T) {
	doc := &model.Document{ID: uuid.NewString(), Name: "file.docx", Owner: "owner", Repository: "repo", Branch: "main", BaseBranch: "main", OriginalBucket: "bucket", OriginalObjectKey: "org/p.docx", Status: model.StatusUploaded}
	repo := &repoMock{doc: doc}
	svc := New(repo, &keyMock{}, nil, "orig", "md", "owner", "repo", "main", "main")

	returned, err := svc.ConfirmUpload(context.Background(), ConfirmUploadInput{DocumentID: doc.ID, Bucket: "bucket", ObjectKey: "org/p.docx"})
	require.NoError(t, err)
	require.Equal(t, doc, returned)
	require.Empty(t, repo.savedStatus)
}

func TestUpdateStatusValidation(t *testing.T) {
	svc := New(&repoMock{doc: &model.Document{ID: uuid.NewString()}}, &keyMock{}, nil, "orig", "md", "owner", "repo", "main", "main")
	_, err := svc.UpdateStatus(context.Background(), UpdateStatusInput{DocumentID: "bad", Status: model.StatusFailed})
	require.Error(t, err)
}

func TestUpdateStatusDelegates(t *testing.T) {
	repo := &repoMock{doc: &model.Document{ID: uuid.NewString(), Status: model.StatusUploaded}}
	svc := New(repo, &keyMock{}, nil, "orig", "md", "owner", "repo", "main", "main")
	doc, err := svc.UpdateStatus(context.Background(), UpdateStatusInput{DocumentID: repo.doc.ID, Status: model.StatusStoredOriginal})
	require.NoError(t, err)
	require.Equal(t, model.StatusStoredOriginal, doc.Status)
	require.Equal(t, model.StatusStoredOriginal, repo.savedStatus)
}

func TestUpdateStatusSavesErrorReason(t *testing.T) {
	repo := &repoMock{doc: &model.Document{ID: uuid.NewString(), Status: model.StatusUploaded}}
	svc := New(repo, &keyMock{}, nil, "orig", "md", "owner", "repo", "main", "main")

	doc, err := svc.UpdateStatus(context.Background(), UpdateStatusInput{DocumentID: repo.doc.ID, Status: model.StatusFailed, Reason: "boom"})
	require.NoError(t, err)
	require.Equal(t, "boom", repo.savedError)
	require.Equal(t, "boom", doc.ErrorReason)
}

func TestCreateDocumentKeyFailure(t *testing.T) {
	svc := New(&repoMock{}, failingKey{}, nil, "a", "b", "owner", "repo", "main", "main")
	_, err := svc.CreateDocument(context.Background(), CreateInput{FileName: "name"})
	require.Error(t, err)
}
