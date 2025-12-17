// backend/docling-transformer-service/internal/usecase/types.go
package usecase

type TaskState string

const (
	StateProcessing     TaskState = "processing"
	StateWaitingDocling           = "waiting_docling"
	StateRescheduled              = "rescheduled" // задача переведена обратно в pending внутри обработки (overlay base→ocr)
	StateCompleted                = "completed"
)
