package persistence

import (
	"context"
	"regexp"
	"testing"

	"github.com/jackc/pgx/v5"
	pgxmock "github.com/pashagolub/pgxmock/v3"

	"backend-storage-service/internal/domain"
)

func TestDocumentRepository_MarkStatus(t *testing.T) {
	ctx := context.Background()
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer mock.Close()

	repo := NewDocumentRepository()
	mock.ExpectBeginTx(pgx.TxOptions{})
	tx, err := mock.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}

	mock.ExpectExec(regexp.QuoteMeta("UPDATE documents SET status=$1, actual_status=$1, error_reason='', updated_at=now() WHERE id=$2")).
		WithArgs(domain.DocumentStatusStoredOriginal, "doc").
		WillReturnResult(pgxmock.NewResult("UPDATE", 1))
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO document_status_logs (id, document_id, from_status, to_status, workflow_id, reason, created_at) VALUES (gen_random_uuid(), $1, NULL, $2, $3, $4, now())")).
		WithArgs("doc", domain.DocumentStatusStoredOriginal, "corr", "reason").
		WillReturnResult(pgxmock.NewResult("INSERT", 1))
	mock.ExpectCommit()

	payload := map[string]any{"correlation_id": "corr"}
	if err := repo.MarkStatus(ctx, tx, "doc", domain.DocumentStatusStoredOriginal, payload, "reason"); err != nil {
		t.Fatalf("mark status: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}
