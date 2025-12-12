package server

import (
	"net/http"

	"go.uber.org/zap"
)

func (s *Server) handleStorageNotification(w http.ResponseWriter, r *http.Request) {
	report, err := s.uploadConfirmationUC.HandleWebhook(r.Context(), r.Body)
	if err != nil {
		s.logger.Warn("cannot handle storage notification", zap.Error(err))
		http.Error(w, "invalid payload", http.StatusBadRequest)
		return
	}

	for _, result := range report.Results {
		logger := s.logger.With(
			zap.String("bucket", result.Bucket),
			zap.String("object_key", result.ObjectKey),
		)

		if result.CorrelationID != "" {
			logger = logger.With(zap.String("correlation_id", result.CorrelationID))
		}

		if result.DocumentID != "" {
			logger = logger.With(zap.String("document_id", result.DocumentID))
		}

		if result.Err != nil {
			logger.Warn("failed to process upload notification", zap.Error(result.Err))
			continue
		}

		logger.Info("upload confirmed")
	}

	if report.ProcessedCount() == 0 {
		http.Error(w, "no records processed", http.StatusBadRequest)
		return
	}

	w.WriteHeader(http.StatusAccepted)
}
