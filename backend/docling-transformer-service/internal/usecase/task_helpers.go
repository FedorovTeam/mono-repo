package usecase

import (
	"errors"
	"fmt"
	"strings"

	transformations "github.com/mono-repo/docling-transformer-service/internal/domain/transformations"
)

func normalizeMediaType(mediaType, charset string) string {
	mediaType = strings.TrimSpace(mediaType)
	if mediaType == "" {
		mediaType = "text/markdown"
	}

	lower := strings.ToLower(mediaType)
	if strings.Contains(lower, "charset=") {
		return mediaType
	}

	charset = strings.TrimSpace(charset)
	if charset == "" {
		charset = "utf-8"
	}

	return mediaType + "; charset=" + charset
}

func buildFailureMessage(err error, task *transformations.TransformationTask) string {
	if task == nil {
		return err.Error()
	}

	jobID := task.DoclingJobID
	pollCount := task.DoclingPollCount

	switch {
	case errors.Is(err, transformations.ErrPollingAttemptsExceeded):
		return fmt.Sprintf("docling polling attempts exceeded for job %s after %d attempts", jobID, pollCount)
	case jobID != "":
		return fmt.Sprintf("docling job %s failed: %v", jobID, err)
	default:
		return err.Error()
	}
}
