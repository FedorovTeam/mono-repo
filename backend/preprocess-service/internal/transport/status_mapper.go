package transport

import (
	"preprocess-service/internal/domain"
	preprocessv1 "preprocess-service/proto/backend/preprocess/v1"
)

func toProtoStatus(status domain.PreprocessStatus) preprocessv1.PreprocessStatus {
	switch status {
	case domain.StatusProcessing:
		return preprocessv1.PreprocessStatus_PREPROCESS_STATUS_PROCESSING
	case domain.StatusCompleted:
		return preprocessv1.PreprocessStatus_PREPROCESS_STATUS_COMPLETED
	case domain.StatusFailed:
		return preprocessv1.PreprocessStatus_PREPROCESS_STATUS_FAILED
	case domain.StatusStopped:
		return preprocessv1.PreprocessStatus_PREPROCESS_STATUS_STOPPED
	case domain.StatusPending:
		return preprocessv1.PreprocessStatus_PREPROCESS_STATUS_PENDING
	default:
		return preprocessv1.PreprocessStatus_PREPROCESS_STATUS_UNSPECIFIED
	}
}

func statusFromProto(status preprocessv1.PreprocessStatus) domain.PreprocessStatus {
	switch status {
	case preprocessv1.PreprocessStatus_PREPROCESS_STATUS_PROCESSING:
		return domain.StatusProcessing
	case preprocessv1.PreprocessStatus_PREPROCESS_STATUS_COMPLETED:
		return domain.StatusCompleted
	case preprocessv1.PreprocessStatus_PREPROCESS_STATUS_FAILED:
		return domain.StatusFailed
	case preprocessv1.PreprocessStatus_PREPROCESS_STATUS_STOPPED:
		return domain.StatusStopped
	case preprocessv1.PreprocessStatus_PREPROCESS_STATUS_PENDING:
		return domain.StatusPending
	default:
		return domain.StatusUnspecified
	}
}

func toProtoStage(stage domain.ProcessingStage) preprocessv1.PreprocessStage {
	switch stage {
	case domain.StageDownloading:
		return preprocessv1.PreprocessStage_PREPROCESS_STAGE_DOWNLOADING
	case domain.StageConverting:
		return preprocessv1.PreprocessStage_PREPROCESS_STAGE_CONVERTING
	case domain.StageUploading:
		return preprocessv1.PreprocessStage_PREPROCESS_STAGE_UPLOADING
	case domain.StageCallback:
		return preprocessv1.PreprocessStage_PREPROCESS_STAGE_CALLBACK
	default:
		return preprocessv1.PreprocessStage_PREPROCESS_STAGE_UNSPECIFIED
	}
}
