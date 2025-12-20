package postgres

import "preprocess-service/internal/domain"

func toStatusString(status domain.PreprocessStatus) string {
	switch status {
	case domain.StatusProcessing:
		return "processing"
	case domain.StatusCompleted:
		return "done"
	case domain.StatusFailed:
		return "failed"
	case domain.StatusStopped:
		return "stopped"
	case domain.StatusPending, domain.StatusUnspecified:
		return "pending"
	default:
		return "pending"
	}
}

func statusFromString(value string) domain.PreprocessStatus {
	switch value {
	case "pending":
		return domain.StatusPending
	case "processing":
		return domain.StatusProcessing
	case "done":
		return domain.StatusCompleted
	case "failed":
		return domain.StatusFailed
	case "stopped":
		return domain.StatusStopped
	default:
		return domain.StatusUnspecified
	}
}
