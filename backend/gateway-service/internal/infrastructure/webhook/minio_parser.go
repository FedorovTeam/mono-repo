package webhook

import (
	"context"
	"encoding/json"
	"io"

	"github.com/minio/minio-go/v7/pkg/notification"

	"backend-gateway-service/internal/usecase"
)

// MinIOWebhookParser разбирает payload вебхука от MinIO.
type MinIOWebhookParser struct{}

// NewMinIOWebhookParser создаёт парсер вебхуков MinIO.
func NewMinIOWebhookParser() *MinIOWebhookParser {
	return &MinIOWebhookParser{}
}

// Parse декодирует json-payload и возвращает список уведомлений о загрузке.
func (p *MinIOWebhookParser) Parse(_ context.Context, body io.Reader) ([]usecase.StorageNotification, error) {
	var payload notification.Info
	if err := json.NewDecoder(body).Decode(&payload); err != nil {
		return nil, err
	}

	notifications := make([]usecase.StorageNotification, 0, len(payload.Records))
	for _, record := range payload.Records {
		notifications = append(notifications, usecase.StorageNotification{
			Bucket:           record.S3.Bucket.Name,
			ObjectKey:        record.S3.Object.Key,
			UserMetadata:     record.S3.Object.UserMetadata,
			ResponseElements: record.ResponseElements,
		})
	}

	return notifications, nil
}
