package domain

// ProcessingStage описывает этап обработки задачи.
// Значения совпадают со строковым представлением в БД.
type ProcessingStage string

const (
	StageUnspecified ProcessingStage = ""
	StageDownloading ProcessingStage = "downloading"
	StageConverting  ProcessingStage = "converting"
	StageUploading   ProcessingStage = "uploading"
	StageCallback    ProcessingStage = "callback"
)

const (
	StageDownloadingString = string(StageDownloading)
	StageConvertingString  = string(StageConverting)
	StageUploadingString   = string(StageUploading)
	StageCallbackString    = string(StageCallback)
)

func (s ProcessingStage) String() string {
	return string(s)
}

func (s ProcessingStage) IsValid() bool {
	switch s {
	case StageDownloading, StageConverting, StageUploading, StageCallback:
		return true
	default:
		return false
	}
}
