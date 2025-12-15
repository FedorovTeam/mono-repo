package document

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"

	"backend-document-service/internal/domain"
)

type documentStatusRepo interface {
	GetDocument(ctx context.Context, id string) (*domain.Document, error)
	UpdateStatus(ctx context.Context, id string, to domain.DocumentStatus, reason, errorReason string, version *domain.DocumentVersion) (*domain.Document, error)
	LastNonFailedStatus(ctx context.Context, id string) (domain.DocumentStatus, error)
}

// StatusApplication инкапсулирует обновление статусов документов и запись логов.
type StatusApplication struct {
	repo documentStatusRepo
	logf func(format string, args ...interface{})
}

func NewStatusApplication(repo documentStatusRepo) *StatusApplication {
	return &StatusApplication{repo: repo, logf: log.Printf}
}

// TransitionByID получает документ и выполняет переход с записью логов статуса.
func (s *StatusApplication) TransitionByID(ctx context.Context, documentID string, to domain.DocumentStatus, reason, errorReason string, version *domain.DocumentVersion) (*domain.Document, error) {
	doc, err := s.repo.GetDocument(ctx, documentID)
	if err != nil {
		return nil, err
	}
	if doc == nil {
		return nil, fmt.Errorf("document %s not found", documentID)
	}
	return s.Transition(ctx, doc, to, reason, errorReason, version)
}

// Transition выполняет переход статуса документа и единообразно пишет события.
func (s *StatusApplication) Transition(ctx context.Context, doc *domain.Document, to domain.DocumentStatus, reason, errorReason string, version *domain.DocumentVersion) (*domain.Document, error) {
	if doc == nil {
		return nil, errors.New("document is required")
	}
	current, err := s.ResolveCurrentStatus(ctx, doc)
	if err != nil {
		return nil, err
	}
	if err := domain.ValidateDocumentStatusTransition(current, to); err != nil {
		return nil, err
	}

	updated, err := s.repo.UpdateStatus(ctx, doc.ID, to, reason, errorReason, version)
	if err == nil {
		s.logf("document %s transitioned %s -> %s: %s", doc.ID, current, to, reason)
	}
	return updated, err
}

// Fail фиксирует падение документа и обновляет лог статусов.
func (s *StatusApplication) Fail(ctx context.Context, documentID, reason string) error {
	_, err := s.TransitionByID(ctx, documentID, domain.DocumentStatusFailed, reason, reason, nil)
	return err
}

// ResolveCurrentStatus возвращает фактический этап обработки документа.
// Если документ в статусе Failed, метод ищет последний успешный статус в логах,
// чтобы повторный запуск продолжал цепочку с корректного шага.
func (s *StatusApplication) ResolveCurrentStatus(ctx context.Context, doc *domain.Document) (domain.DocumentStatus, error) {
	if doc == nil {
		return "", errors.New("document is required")
	}
	if doc.Status != domain.DocumentStatusFailed {
		return domain.DocumentStatus(doc.Status), nil
	}

	statusVal, err := s.repo.LastNonFailedStatus(ctx, doc.ID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return domain.DocumentStatusUploaded, nil
		}
		return "", err
	}

	if statusVal == "" {
		return domain.DocumentStatusUploaded, nil
	}

	return statusVal, nil
}
