package usecase

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"path"
	"strings"

	"backend-gateway-service/internal/ctxutils"
	"backend-gateway-service/internal/domain"
)

// StorageNotification описывает событие о загруженном объекте в хранилище.
type StorageNotification struct {
	Bucket           string
	ObjectKey        string
	UserMetadata     map[string]string
	ResponseElements map[string]string
}

// StorageNotificationParser отвечает за разбор входящих вебхуков из MinIO.
type StorageNotificationParser interface {
	Parse(ctx context.Context, body io.Reader) ([]StorageNotification, error)
}

// UploadConfirmationPort описывает порт подтверждения загрузки документа.
type UploadConfirmationPort interface {
	ConfirmUpload(ctx context.Context, req domain.ConfirmUploadRequest) (*domain.Document, error)
}

// UploadConfirmationResult хранит результат обработки одного уведомления.
type UploadConfirmationResult struct {
	Bucket        string
	ObjectKey     string
	DocumentID    string
	CorrelationID string
	Err           error
}

// Processed возвращает true, если уведомление успешно обработано.
func (r UploadConfirmationResult) Processed() bool {
	return r.Err == nil && r.DocumentID != ""
}

// UploadConfirmationHandler описывает сценарий обработки webhook-уведомлений.
type UploadConfirmationHandler interface {
	HandleWebhook(ctx context.Context, body io.Reader) (UploadConfirmationReport, error)
}

// UploadConfirmationReport содержит результаты обработки всех уведомлений.
type UploadConfirmationReport struct {
	Results []UploadConfirmationResult
}

// ProcessedCount возвращает количество успешно обработанных уведомлений.
func (r UploadConfirmationReport) ProcessedCount() int {
	count := 0
	for _, result := range r.Results {
		if result.Processed() {
			count++
		}
	}
	return count
}

// UploadConfirmationService отвечает за подтверждение загрузки документов по вебхуку MinIO.
type UploadConfirmationService struct {
	parser    StorageNotificationParser
	documents UploadConfirmationPort
}

// NewUploadConfirmationService создаёт UploadConfirmationService.
func NewUploadConfirmationService(parser StorageNotificationParser, documents UploadConfirmationPort) *UploadConfirmationService {
	return &UploadConfirmationService{parser: parser, documents: documents}
}

// HandleWebhook обрабатывает входящий вебхук MinIO и подтверждает загрузку документов.
func (s *UploadConfirmationService) HandleWebhook(ctx context.Context, body io.Reader) (UploadConfirmationReport, error) {
	events, err := s.parser.Parse(ctx, body)
	if err != nil {
		return UploadConfirmationReport{}, fmt.Errorf("cannot parse webhook payload: %w", err)
	}

	report := UploadConfirmationReport{Results: make([]UploadConfirmationResult, 0, len(events))}

	for _, event := range events {
		result := UploadConfirmationResult{Bucket: event.Bucket}

		correlationID := extractCorrelationID(event)
		eventCtx := ctx
		if correlationID != "" {
			eventCtx = ctxutils.WithCorrelationID(eventCtx, correlationID)
		}
		eventCtx, ensuredCorrelationID := ctxutils.EnsureCorrelationID(eventCtx)
		result.CorrelationID = ensuredCorrelationID

		decodedKey, err := url.PathUnescape(event.ObjectKey)
		if err != nil {
			result.Err = fmt.Errorf("cannot decode object key %q: %w", event.ObjectKey, err)
			report.Results = append(report.Results, result)
			continue
		}
		result.ObjectKey = decodedKey

		documentID := extractDocumentID(decodedKey)
		if documentID == "" {
			result.Err = errors.New("document id is empty")
			report.Results = append(report.Results, result)
			continue
		}
		result.DocumentID = documentID

		_, err = s.documents.ConfirmUpload(eventCtx, domain.ConfirmUploadRequest{
			DocumentID:    documentID,
			Bucket:        event.Bucket,
			ObjectKey:     decodedKey,
			CorrelationID: ensuredCorrelationID,
		})
		if err != nil {
			result.Err = fmt.Errorf("cannot confirm upload: %w", err)
		}

		report.Results = append(report.Results, result)
	}

	return report, nil
}

func extractDocumentID(objectKey string) string {
	base := path.Base(objectKey)
	if base == "." || base == ".." || base == "/" {
		return ""
	}
	parts := strings.SplitN(base, ".", 2)
	if len(parts) == 0 {
		return ""
	}
	return parts[0]
}

func extractCorrelationID(event StorageNotification) string {
	for key, value := range event.UserMetadata {
		if strings.EqualFold(key, "x-amz-meta-correlation-id") {
			return value
		}
	}
	for key, value := range event.ResponseElements {
		if strings.EqualFold(key, "x-amz-meta-correlation-id") {
			return value
		}
	}
	return ""
}
