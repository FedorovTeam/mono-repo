package persistence

import (
	"database/sql"

	"backend-document-service/internal/repository"
)

// DocumentRepository адаптирует SQL-репозиторий под порты usecase.
type DocumentRepository struct {
	*repository.Repository
}

func NewDocumentRepository(db *sql.DB) *DocumentRepository {
	return &DocumentRepository{Repository: repository.New(db)}
}
