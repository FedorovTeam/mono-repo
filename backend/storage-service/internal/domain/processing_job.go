package domain

import "time"

// ProcessingJob описывает задачу обработки документа.
type ProcessingJob struct {
	TaskID     string
	DocumentID string
	Stage      string
	Status     string
	Attempts   int
	NextRunAt  time.Time
	Payload    map[string]any
}
