// backend/docling-transformer-service/internal/usecase/task_helpers.go
package usecase

import (
	"errors"
	"fmt"
	"strings"

	"github.com/mono-repo/docling-transformer-service/internal/domain"
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

	ids := domain.DoclingIdentifiers{TaskID: task.DoclingTaskID, JobID: task.DoclingJobID}
	primaryID := ids.PrimaryID()
	pollCount := task.DoclingPollCount

	var restartErr *transformations.DoclingRestartsExceededError
	switch {
	case errors.As(err, &restartErr):
		reason := "docling restarts limit exceeded"
		if restartErr != nil {
			if restartErr.MaxRestarts > 0 {
				reason = fmt.Sprintf("docling restarts exceeded after %d/%d restarts", restartErr.Restarts, restartErr.MaxRestarts)
			} else {
				reason = fmt.Sprintf("docling restarts exceeded after %d restarts", restartErr.Restarts)
			}
		}

		if primaryID != "" {
			return fmt.Sprintf("docling task %s failed: %s", primaryID, reason)
		}

		return reason
	case errors.Is(err, transformations.ErrPollingAttemptsExceeded):
		return fmt.Sprintf("docling polling attempts exceeded for task %s after %d attempts", primaryID, pollCount)
	case primaryID != "":
		return fmt.Sprintf("docling task %s failed: %v", primaryID, err)
	default:
		return err.Error()
	}
}
