package document

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"backend-document-service/internal/model"
)

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
	confirm := &confirmUploadMock{}
	reconciler := NewUploadReconciler(repo, confirm, 10, time.Minute)
	reconciler.now = func() time.Time { return fixed }

	reconciler.reconcile(context.Background())

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
	confirm := &confirmUploadMock{}
	reconciler := NewUploadReconciler(repo, confirm, 10, time.Minute)
	reconciler.now = func() time.Time { return fixed }

	reconciler.reconcile(context.Background())

	require.Zero(t, confirm.calls)
}

func TestUploadReconcilerSkipsWhenNoObjectKey(t *testing.T) {
	fixed := time.Date(2024, 12, 1, 12, 0, 0, 0, time.UTC)
	doc := model.Document{
		ID:                uuid.NewString(),
		OriginalBucket:    "",
		OriginalObjectKey: "",
		Status:            model.StatusUploaded,
		UpdatedAt:         fixed.Add(-2 * time.Minute),
	}
	repo := &repoMock{doc: &doc, docs: []model.Document{doc}}
	confirm := &confirmUploadMock{}
	reconciler := NewUploadReconciler(repo, confirm, 10, time.Minute)
	reconciler.now = func() time.Time { return fixed }

	reconciler.reconcile(context.Background())

	require.Zero(t, confirm.calls)
}
