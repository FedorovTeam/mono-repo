package migrations

import (
	"context"
	"regexp"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"
)

func TestRunMigrations(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	mock.MatchExpectationsInOrder(false)
	mock.ExpectQuery("SELECT version_id, is_applied.*schema_migrations_document_service").
		WillReturnRows(sqlmock.NewRows([]string{"version_id", "is_applied"}).AddRow(0, true))
	mock.ExpectQuery("SELECT version_id, is_applied.*schema_migrations_document_service").
		WillReturnRows(sqlmock.NewRows([]string{"version_id", "is_applied"}).AddRow(0, true))

	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta("CREATE TABLE IF NOT EXISTS documents (")).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(regexp.QuoteMeta("CREATE TABLE IF NOT EXISTS document_versions (")).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(regexp.QuoteMeta("CREATE INDEX IF NOT EXISTS idx_document_versions_doc ON document_versions(document_id)")).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(regexp.QuoteMeta("CREATE TABLE IF NOT EXISTS document_status_logs (")).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(regexp.QuoteMeta("CREATE INDEX IF NOT EXISTS idx_document_status_logs_doc ON document_status_logs(document_id)")).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("INSERT INTO schema_migrations_document_service").
		WithArgs(sqlmock.AnyArg(), sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()

	err = Run(context.Background(), db)
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}
