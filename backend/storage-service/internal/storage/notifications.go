package storage

import (
	"context"
	"log"
	"path"
	"strings"

	"github.com/minio/minio-go/v7/pkg/notification"
	"google.golang.org/grpc"

	documentv1 "backend-storage-service/proto/backend/document/v1"
)

// DocumentNotifier описывает клиент DocumentService, который подтверждает загрузку.
type DocumentNotifier interface {
	ConfirmUpload(ctx context.Context, in *documentv1.ConfirmUploadRequest, opts ...grpc.CallOption) (*documentv1.Document, error)
}

// UploadEventHandler обрабатывает события MinIO и уведомляет document-service.
type UploadEventHandler struct {
	documentClient DocumentNotifier
	logger         *log.Logger
}

func NewUploadEventHandler(documentClient DocumentNotifier, logger *log.Logger) *UploadEventHandler {
	if logger == nil {
		logger = log.Default()
	}
	return &UploadEventHandler{documentClient: documentClient, logger: logger}
}

// ListenAndForward подписывается на события загрузки и вызывает ConfirmUpload.
func ListenAndForward(ctx context.Context, client Client, bucket string, handler *UploadEventHandler) {
	events := []string{"s3:ObjectCreated:*"}
	ch := client.ListenBucketNotification(ctx, bucket, "", "", events)

	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case info, ok := <-ch:
				if !ok {
					return
				}
				handler.handleNotification(ctx, info)
			}
		}
	}()
}

func (h *UploadEventHandler) handleNotification(ctx context.Context, info notification.Info) {
	if h == nil || h.documentClient == nil {
		return
	}
	for _, record := range info.Records {
		key := record.S3.Object.Key
		documentID := extractDocumentID(key)
		if documentID == "" {
			h.logger.Printf("skip notification without document id: key=%s", key)
			continue
		}

		_, err := h.documentClient.ConfirmUpload(ctx, &documentv1.ConfirmUploadRequest{
			DocumentId: documentID,
			Bucket:     record.S3.Bucket.Name,
			ObjectKey:  key,
		})
		if err != nil {
			h.logger.Printf("confirm upload failed: document_id=%s key=%s error=%v", documentID, key, err)
		} else {
			h.logger.Printf("upload confirmed: document_id=%s key=%s", documentID, key)
		}
	}
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
