package domain

import "time"

// ProcessingJob описывает задачу фоновой обработки документа.
type ProcessingJob struct {
	TaskID     string
	DocumentID string
	Stage      JobStage
	Status     JobStatus
	Attempts   int
	NextRunAt  time.Time
	// Payload хранит ссылки для внешних исполнителей (bucket/object_key, queue_id и т.п.).
	Payload   map[string]any
	CreatedAt time.Time
	UpdatedAt time.Time
}

// JobEvent фиксирует события обработки задачи.
type JobEvent struct {
	ID        string
	JobID     string
	Stage     JobStage
	Status    JobStatus
	EventType string
	Message   string
	// Payload события фиксирует входные/выходные ссылки шагов (например, object_key, queue_id или commit_sha) без размещения контента.
	Payload   map[string]any
	CreatedAt time.Time
}

// MarkStatus меняет статус задачи и обновляет временные метки.
func (j *ProcessingJob) MarkStatus(status JobStatus) {
	j.Status = status
	j.UpdatedAt = time.Now().UTC()
}
