package repository

import (
	"context"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"

	"backend-document-service/internal/model"
)

func TestCreateDocument(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	repo := New(db)
	ctx := context.Background()

	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO documents")).
		WithArgs(sqlmock.AnyArg(), "file.docx", string(model.StatusUploaded), "orig", "orig/key", "md", "md/key", sqlmock.AnyArg(), sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO document_versions")).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO document_status_logs")).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()

	doc := &model.Document{
		Name:              "file.docx",
		Status:            model.StatusUploaded,
		OriginalBucket:    "orig",
		OriginalObjectKey: "orig/key",
		MarkdownBucket:    "md",
		MarkdownObjectKey: "md/key",
	}
	version := &model.DocumentVersion{Version: 1, OriginalObjectKey: "orig/key", MarkdownObjectKey: "md/key"}
	log := &model.DocumentStatusLog{ToStatus: model.StatusUploaded}

	saved, err := repo.CreateDocument(ctx, doc, version, log)
	require.NoError(t, err)
	require.NotEmpty(t, saved.ID)
	require.Len(t, saved.Versions, 1)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestUpdateStatusFlow(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	repo := New(db)
	ctx := context.Background()

	now := time.Now()
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT id, name, status").
		WithArgs("doc-1").
		WillReturnRows(sqlmock.NewRows([]string{"id", "name", "status", "original_bucket", "original_object_key", "markdown_bucket", "markdown_object_key", "created_at", "updated_at"}).
			AddRow("doc-1", "name", model.StatusUploaded, "orig", "orig/key", "md", "md/key", now, now))
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO document_versions")).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec(regexp.QuoteMeta("UPDATE documents SET status=$1, markdown_object_key=$2, updated_at=$3 WHERE id=$4")).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO document_status_logs")).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectQuery("SELECT id, document_id, version").
		WithArgs("doc-1").
		WillReturnRows(sqlmock.NewRows([]string{"id", "document_id", "version", "original_object_key", "markdown_object_key", "created_at", "updated_at"}).
			AddRow("ver-1", "doc-1", 1, "orig/key", "md/key", now, now).
			AddRow("ver-2", "doc-1", 2, "orig/key2", "md/key2", now, now))
	mock.ExpectCommit()

	version := &model.DocumentVersion{Version: 2, OriginalObjectKey: "orig/key2", MarkdownObjectKey: "md/key2"}
	doc, err := repo.UpdateStatus(ctx, "doc-1", model.StatusStoredOriginal, "stored", version)
	require.NoError(t, err)
	require.Equal(t, model.StatusStoredOriginal, doc.Status)
	require.Len(t, doc.Versions, 2)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestListDocuments(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	repo := New(db)
	ctx := context.Background()

	mock.ExpectQuery("SELECT id, name, status").
		WillReturnRows(sqlmock.NewRows([]string{"id", "name", "status", "original_bucket", "original_object_key", "markdown_bucket", "markdown_object_key", "created_at", "updated_at"}).
			AddRow("doc-1", "name", model.StatusUploaded, "orig", "orig/key", "md", "md/key", time.Now(), time.Now()))

	docs, err := repo.ListDocuments(ctx, 0, 0, nil)
	require.NoError(t, err)
	require.Len(t, docs, 1)
	require.NoError(t, mock.ExpectationsWereMet())
}
