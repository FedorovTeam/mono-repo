package repository

import (
	"context"

	"github.com/google/uuid"

	"github.com/mono-repo/table-extractor-service/internal/domain"
)

type TableExtractionRepository interface {
	Create(ctx context.Context, job domain.TableExtractionJob) (domain.TableExtractionJob, error)
	GetByDocumentID(ctx context.Context, documentID string) (domain.TableExtractionJob, bool, error)
	GetByID(ctx context.Context, id uuid.UUID) (domain.TableExtractionJob, bool, error)
	CountActive(ctx context.Context) (int, error)
}
