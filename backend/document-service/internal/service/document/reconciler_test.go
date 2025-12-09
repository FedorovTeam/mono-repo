package document

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/minio/minio-go/v7"
	"github.com/stretchr/testify/require"

	"backend-document-service/internal/model"
)

type statObjectMock struct {
	calls int
	err   error
}

func (s *statObjectMock) StatObject(ctx context.Context, bucketName, objectName string, opts minio.StatObjectOptions) (minio.ObjectInfo, error) {
	s.calls++
	return minio.ObjectInfo{}, s.err
}

type confirmUploadMock struct {
	calls     int
	lastInput ConfirmUploadInput
}

func (c *confirmUploadMock) ConfirmUpload(ctx context.Context, in ConfirmUploadInput) (*model.Document, error) {
	c.calls++
	c.lastInput = in
	return &model.Document{ID: in.DocumentID}, nil
}

func TestUploadReconcilerConfirmsExistingObjects(t *testing.T) {
	fixed := time.Date(2024, 12, 1, 12, 0, 0, 0, time.UTC)
	doc := model.Document{
		ID:                uuid.NewString(),
		OriginalBucket:    "bucket",
		OriginalObjectKey: "path/file.docx",
		Status:            model.StatusUploaded,
		UpdatedAt:         fixed.Add(-2 * time.Minute),
	}
	repo := &repoMock{doc: &doc, docs: []model.Document{doc}}
	stat := &statObjectMock{}
	confirm := &confirmUploadMock{}
	reconciler := NewUploadReconciler(repo, stat, confirm, 10, time.Minute)
	reconciler.now = func() time.Time { return fixed }

	reconciler.reconcile(context.Background())

	require.Equal(t, 1, stat.calls)
	require.Equal(t, 1, confirm.calls)
	require.Equal(t, doc.ID, confirm.lastInput.DocumentID)
}

func TestUploadReconcilerSkipsRecentDocuments(t *testing.T) {
	fixed := time.Date(2024, 12, 1, 12, 0, 0, 0, time.UTC)
	doc := model.Document{
		ID:                uuid.NewString(),
		OriginalBucket:    "bucket",
		OriginalObjectKey: "path/file.docx",
		Status:            model.StatusUploaded,
		UpdatedAt:         fixed.Add(-30 * time.Second),
	}
	repo := &repoMock{doc: &doc, docs: []model.Document{doc}}
	stat := &statObjectMock{}
	confirm := &confirmUploadMock{}
	reconciler := NewUploadReconciler(repo, stat, confirm, 10, time.Minute)
	reconciler.now = func() time.Time { return fixed }

	reconciler.reconcile(context.Background())

	require.Zero(t, stat.calls)
	require.Zero(t, confirm.calls)
}

func TestUploadReconcilerSkipsMissingObjects(t *testing.T) {
	fixed := time.Date(2024, 12, 1, 12, 0, 0, 0, time.UTC)
	doc := model.Document{
		ID:                uuid.NewString(),
		OriginalBucket:    "bucket",
		OriginalObjectKey: "path/file.docx",
		Status:            model.StatusUploaded,
		UpdatedAt:         fixed.Add(-2 * time.Minute),
	}
	repo := &repoMock{doc: &doc, docs: []model.Document{doc}}
	stat := &statObjectMock{err: minio.ErrorResponse{Code: "NoSuchKey"}}
	confirm := &confirmUploadMock{}
	reconciler := NewUploadReconciler(repo, stat, confirm, 10, time.Minute)
	reconciler.now = func() time.Time { return fixed }

	reconciler.reconcile(context.Background())

	require.Equal(t, 1, stat.calls)
	require.Zero(t, confirm.calls)
}
