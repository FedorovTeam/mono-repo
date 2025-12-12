package persistence

import (
	"context"

	"backend-storage-service/internal/application"
	"backend-storage-service/internal/domain"
)

// DocumentRepository реализует application.DocumentRepository через pgx.
type DocumentRepository struct{}

// NewDocumentRepository создаёт репозиторий документов.
func NewDocumentRepository() *DocumentRepository {
	return &DocumentRepository{}
}

// MarkStatus обновляет состояние документа и пишет запись в лог статусов.
func (r *DocumentRepository) MarkStatus(ctx context.Context, tx application.Tx, documentID string, status domain.DocumentStatus, payload map[string]any, reason string) error {
	if _, err := tx.Exec(ctx, `
        UPDATE documents
        SET status=$1, actual_status=$1, error_reason='', updated_at=now()
        WHERE id=$2
    `, status, documentID); err != nil {
		return err
	}

	correlationID, _ := payload["correlation_id"].(string)
	_, err := tx.Exec(ctx, `
        INSERT INTO document_status_logs (id, document_id, from_status, to_status, workflow_id, reason, created_at)
        VALUES (gen_random_uuid(), $1, NULL, $2, $3, $4, now())
    `, documentID, status, correlationID, reason)
	return err
}
