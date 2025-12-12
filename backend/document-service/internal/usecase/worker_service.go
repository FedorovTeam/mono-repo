package usecase

import (
	"context"
	"log"
	"time"

	"backend-document-service/internal/domain"
)

type JobResult struct {
	Status     domain.JobStatus
	Payload    map[string]any
	RetryAfter time.Duration
	Event      *domain.JobEvent
}

type JobHandler func(ctx context.Context, job domain.ProcessingJob) JobResult

// WorkerHooks содержит необязательные колбэки для метрик и логирования.
type WorkerHooks struct {
	Batch func(ctx context.Context, size int)
	Error func(ctx context.Context)
}

type WorkerService struct {
	jobs      domain.JobRepository
	batchSize int
	logf      func(format string, args ...interface{})
	hooks     WorkerHooks
}

func NewWorkerService(jobs domain.JobRepository, batchSize int, hooks WorkerHooks) *WorkerService {
	if batchSize <= 0 {
		batchSize = 100
	}

	return &WorkerService{
		jobs:      jobs,
		batchSize: batchSize,
		logf:      log.Printf,
		hooks:     hooks,
	}
}

func (s *WorkerService) Run(ctx context.Context, stage domain.JobStage, interval time.Duration, handler JobHandler) {
	if interval <= 0 {
		interval = time.Minute
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	s.Process(ctx, stage, handler)

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.Process(ctx, stage, handler)
		}
	}
}

// Process выполняет один цикл обработки доступных задач указанной стадии.
func (s *WorkerService) Process(ctx context.Context, stage domain.JobStage, handler JobHandler) {
	for {
		jobs, err := s.jobs.AcquirePendingJobs(ctx, stage, s.batchSize)
		if err != nil {
			s.logf("worker[%s]: acquire pending jobs: %v", stage, err)
			s.onError(ctx)
			return
		}

		s.onBatch(ctx, len(jobs))
		if len(jobs) == 0 {
			return
		}

		for _, job := range jobs {
			s.handleJob(ctx, job, handler)
		}

		if len(jobs) < s.batchSize {
			return
		}
	}
}

func (s *WorkerService) handleJob(ctx context.Context, job domain.ProcessingJob, handler JobHandler) {
	result := handler(ctx, job)
	status := result.Status
	if status == "" {
		status = domain.JobStatusFailed
	}

	var nextRun *time.Time
	if result.RetryAfter > 0 {
		t := time.Now().Add(result.RetryAfter)
		nextRun = &t
	}

	payload := mergePayload(job.Payload, result.Payload)
	event := prepareEvent(job, status, result.Event)

	if _, err := s.jobs.UpdateProcessingJob(ctx, job.TaskID, status, nextRun, false, payload, event); err != nil {
		s.logf("worker[%s]: update job %s: %v", job.Stage, job.TaskID, err)
		s.onError(ctx)
	}
}

func mergePayload(base, update map[string]any) map[string]any {
	if base == nil && update == nil {
		return nil
	}
	if base == nil {
		cloned := make(map[string]any, len(update))
		for k, v := range update {
			cloned[k] = v
		}
		return cloned
	}
	if update == nil {
		return base
	}
	merged := make(map[string]any, len(base)+len(update))
	for k, v := range base {
		merged[k] = v
	}
	for k, v := range update {
		merged[k] = v
	}
	return merged
}

func prepareEvent(job domain.ProcessingJob, status domain.JobStatus, event *domain.JobEvent) *domain.JobEvent {
	if event == nil {
		return nil
	}
	result := *event
	if result.JobID == "" {
		result.JobID = job.TaskID
	}
	if result.Stage == "" {
		result.Stage = job.Stage
	}
	if result.Status == "" {
		result.Status = status
	}
	if result.Payload == nil {
		result.Payload = map[string]any{}
	}
	return &result
}

func (s *WorkerService) onBatch(ctx context.Context, size int) {
	if s.hooks.Batch != nil {
		s.hooks.Batch(ctx, size)
	}
}

func (s *WorkerService) onError(ctx context.Context) {
	if s.hooks.Error != nil {
		s.hooks.Error(ctx)
	}
}
