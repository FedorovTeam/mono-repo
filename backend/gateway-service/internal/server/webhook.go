package server

import (
	"encoding/json"
	"net/http"
	"net/url"
	"path"
	"strings"

	"github.com/minio/minio-go/v7/pkg/notification"
	"go.uber.org/zap"

	"backend-gateway-service/internal/ctxutils"
	documentpb "backend-gateway-service/proto/backend/document/v1"
)

func (s *Server) handleStorageNotification(w http.ResponseWriter, r *http.Request) {
	var payload notification.Info
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		s.logger.Warn("cannot decode notification", zap.Error(err))
		http.Error(w, "invalid payload", http.StatusBadRequest)
		return
	}

	processed := 0
	for _, record := range payload.Records {
		decodedKey, err := url.PathUnescape(record.S3.Object.Key)
		if err != nil {
			s.logger.Warn("cannot decode object key", zap.String("object_key", record.S3.Object.Key), zap.Error(err))
			continue
		}

		documentID := extractDocumentID(decodedKey)
		if documentID == "" {
			s.logger.Warn("skip notification without document id", zap.String("object_key", decodedKey))
			continue
		}

		correlationID := extractCorrelationID(record)
		ctx := r.Context()
		if correlationID != "" {
			ctx = ctxutils.WithCorrelationID(ctx, correlationID)
		}
		ctx, ensured := ctxutils.EnsureCorrelationID(ctx)
		correlationID = ensured

		logger := s.logger.With(
			zap.String("correlation_id", correlationID),
			zap.String("document_id", documentID),
			zap.String("bucket", record.S3.Bucket.Name),
			zap.String("object_key", decodedKey),
		)

		_, err = s.documentClient.ConfirmUpload(ctx, &documentpb.ConfirmUploadRequest{
			DocumentId:    documentID,
			Bucket:        record.S3.Bucket.Name,
			ObjectKey:     decodedKey,
			CorrelationId: correlationID,
		})
		if err != nil {
			logger.Error("failed to confirm upload", zap.Error(err))
			continue
		}
		processed++
		logger.Info("upload confirmed")
	}

	if processed == 0 {
		http.Error(w, "no records processed", http.StatusBadRequest)
		return
	}

	w.WriteHeader(http.StatusAccepted)
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

func extractCorrelationID(event notification.Event) string {
	for key, value := range event.S3.Object.UserMetadata {
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
