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
	// Payload хранит ссылки для внешних исполнителей (bucket/object_key, queue_id,
	// github_path/repository), а не бинарные данные документов.
	Payload   map[string]any
	CreatedAt time.Time
	UpdatedAt time.Time
}

// JobEvent фиксирует ключевые события обработки.
type JobEvent struct {
	ID        string
	JobID     string
	Stage     JobStage
	Status    JobStatus
	EventType string
	Message   string
	// Payload события фиксирует входные/выходные ссылки шагов (например, object_key,
	// queue_id или commit_sha) без размещения контента.
	Payload   map[string]any
	CreatedAt time.Time
}
