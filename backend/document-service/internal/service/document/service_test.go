package document

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"backend-document-service/internal/model"
	"backend-document-service/internal/storage"
)

type repoMock struct {
	doc         *model.Document
	err         error
	savedStatus model.Status
}

func (r *repoMock) CreateDocument(ctx context.Context, doc *model.Document, version *model.DocumentVersion, log *model.DocumentStatusLog) (*model.Document, error) {
	if r.err != nil {
		return nil, r.err
	}
	doc.ID = uuid.NewString()
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
func (r *repoMock) UpdateStatus(ctx context.Context, id string, to model.Status, reason string, version *model.DocumentVersion) (*model.Document, error) {
	if r.err != nil {
		return nil, r.err
	}
	if r.doc == nil {
		return nil, errors.New("missing doc")
	}
	r.savedStatus = to
	r.doc.Status = to
	return r.doc, nil
}

type keyMock struct{}

func (keyMock) GenerateKeys(ctx context.Context) (storage.KeyPair, error) {
	return storage.KeyPair{OriginalObjectKey: "orig", MarkdownObjectKey: "md"}, nil
}

type failingKey struct{}

func (failingKey) GenerateKeys(ctx context.Context) (storage.KeyPair, error) {
	return storage.KeyPair{}, errors.New("failed")
}

func TestCreateDocumentValidation(t *testing.T) {
	svc := New(&repoMock{}, keyMock{}, nil, "", nil, "", "")
	_, err := svc.CreateDocument(context.Background(), CreateInput{})
	require.Error(t, err)

	_, err = svc.CreateDocument(context.Background(), CreateInput{FileName: "a"})
	require.Error(t, err)

	_, err = svc.CreateDocument(context.Background(), CreateInput{FileName: "a", ID: "bad-uuid"})
	require.Error(t, err)
}

func TestCreateDocumentSuccess(t *testing.T) {
	repo := &repoMock{}
	svc := New(repo, keyMock{}, nil, "", nil, "orig", "md")
	doc, err := svc.CreateDocument(context.Background(), CreateInput{FileName: "name", FilenameExt: "docx"})
	require.NoError(t, err)
	require.NotEmpty(t, doc.ID)
	require.Equal(t, "orig", doc.OriginalBucket)
	require.Equal(t, "md", doc.MarkdownBucket)
	require.Equal(t, model.StatusUploaded, doc.Status)
	require.Len(t, doc.Versions, 1)
}

func TestUpdateStatusValidation(t *testing.T) {
	svc := New(&repoMock{doc: &model.Document{ID: uuid.NewString()}}, keyMock{}, nil, "", nil, "orig", "md")
	_, err := svc.UpdateStatus(context.Background(), UpdateStatusInput{DocumentID: "bad", Status: model.StatusFailed})
	require.Error(t, err)
}

func TestUpdateStatusDelegates(t *testing.T) {
	repo := &repoMock{doc: &model.Document{ID: uuid.NewString(), Status: model.StatusUploaded}}
	svc := New(repo, keyMock{}, nil, "", nil, "orig", "md")
	doc, err := svc.UpdateStatus(context.Background(), UpdateStatusInput{DocumentID: repo.doc.ID, Status: model.StatusStoredOriginal})
	require.NoError(t, err)
	require.Equal(t, model.StatusStoredOriginal, doc.Status)
	require.Equal(t, model.StatusStoredOriginal, repo.savedStatus)
}

func TestCreateDocumentKeyFailure(t *testing.T) {
	svc := New(&repoMock{}, failingKey{}, nil, "", nil, "a", "b")
	_, err := svc.CreateDocument(context.Background(), CreateInput{FileName: "name"})
	require.Error(t, err)
}
