package usecase

import (
	"context"
	"log"
	"time"

	"backend-document-service/internal/domain"
)

// JobResult описывает изменения задачи после выполнения обработчика.
type JobResult struct {
	Payload map[string]any
	NextRun *time.Time
	Event   *domain.JobEvent
	Status  domain.JobStatus
}

// JobHandler инкапсулирует выполнение конкретного этапа обработки.
type JobHandler func(ctx context.Context, job domain.ProcessingJob) (JobResult, error)

// BatchObserver фиксирует размер пачки задач, полученной на обработку.
type BatchObserver func(ctx context.Context, size int)

// WorkerService управляет циклом получения и завершения фоновых задач.
type WorkerService struct {
	jobs   domain.JobRepository
	events domain.EventLogRepository
	retry  time.Duration
	now    func() time.Time
	logf   func(format string, args ...any)
}

// NewWorkerService создаёт сервис обработки задач.
func NewWorkerService(jobs domain.JobRepository, events domain.EventLogRepository, retryDelay time.Duration) *WorkerService {
	if retryDelay <= 0 {
		retryDelay = time.Minute
	}
	return &WorkerService{
		jobs:   jobs,
		events: events,
		retry:  retryDelay,
		now:    time.Now,
		logf:   log.Printf,
	}
}

// SetLogger переопределяет функцию логирования.
func (s *WorkerService) SetLogger(logf func(format string, args ...any)) {
	if logf != nil {
		s.logf = logf
	}
}

// SetClock позволяет использовать тестовые часы.
func (s *WorkerService) SetClock(now func() time.Time) {
	if now != nil {
		s.now = now
	}
}

// Run выполняет периодическую обработку задач указанной стадии.
func (s *WorkerService) Run(ctx context.Context, stage domain.JobStage, interval time.Duration, batchSize int, handler JobHandler, observe BatchObserver) {
	if interval <= 0 {
		interval = time.Minute
	}
	if batchSize <= 0 {
		batchSize = 100
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	s.reconcile(ctx, stage, batchSize, handler, observe)

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.reconcile(ctx, stage, batchSize, handler, observe)
		}
	}
}

// reconcile считывает пачку задач и обрабатывает их до исчерпания очереди.
func (s *WorkerService) reconcile(ctx context.Context, stage domain.JobStage, batchSize int, handler JobHandler, observe BatchObserver) {
	for {
		jobs, err := s.jobs.AcquirePendingJobs(ctx, stage, batchSize)
		if err != nil {
			s.logf("worker[%s]: acquire pending jobs: %v", stage, err)
			return
		}
		if observe != nil {
			observe(ctx, len(jobs))
		}
		if len(jobs) == 0 {
			return
		}

		for _, job := range jobs {
			s.handleJob(ctx, job, handler)
		}

		if len(jobs) < batchSize {
			return
		}
	}
}

func (s *WorkerService) handleJob(ctx context.Context, job domain.ProcessingJob, handler JobHandler) {
	result, err := handler(ctx, job)
	if result.Status == domain.JobStatusPending {
		s.postponeJob(ctx, job, result)
		return
	}
	if err != nil {
		s.failJob(ctx, job, result, err)
		return
	}
	s.completeJob(ctx, job, result)
}

func (s *WorkerService) postponeJob(ctx context.Context, job domain.ProcessingJob, result JobResult) {
	payload := mergePayload(job.Payload, result.Payload)
	nextRun := s.computeNextRun(result.NextRun)
	event := ensureEvent(result.Event, job, domain.JobStatusPending, "postponed", "")

	if _, err := s.jobs.UpdateProcessingJob(ctx, job.TaskID, domain.JobStatusPending, &nextRun, false, payload, nil); err != nil {
		s.logf("worker[%s]: postpone job %s: %v", job.Stage, job.TaskID, err)
	}
	s.appendEvent(ctx, event)
}

func (s *WorkerService) completeJob(ctx context.Context, job domain.ProcessingJob, result JobResult) {
	payload := mergePayload(job.Payload, result.Payload)
	event := ensureEvent(result.Event, job, domain.JobStatusCompleted, "completed", "")

	if _, err := s.jobs.UpdateProcessingJob(ctx, job.TaskID, domain.JobStatusCompleted, nil, false, payload, nil); err != nil {
		s.logf("worker[%s]: update completed job %s: %v", job.Stage, job.TaskID, err)
	}
	s.appendEvent(ctx, event)
}

func (s *WorkerService) failJob(ctx context.Context, job domain.ProcessingJob, result JobResult, err error) {
	payload := mergePayload(job.Payload, result.Payload)
	if payload == nil {
		payload = map[string]any{}
	}
	if _, ok := payload["last_error"]; !ok {
		payload["last_error"] = err.Error()
	}

	nextRun := s.computeNextRun(result.NextRun)

	event := ensureEvent(result.Event, job, domain.JobStatusFailed, "failed", err.Error())

	if _, updateErr := s.jobs.UpdateProcessingJob(ctx, job.TaskID, domain.JobStatusFailed, &nextRun, false, payload, nil); updateErr != nil {
		s.logf("worker[%s]: fail job %s: %v (original: %v)", job.Stage, job.TaskID, updateErr, err)
	}
	s.appendEvent(ctx, event)
}

func (s *WorkerService) computeNextRun(next *time.Time) time.Time {
	if next != nil {
		return *next
	}
	return s.now().Add(s.retry)
}

func (s *WorkerService) appendEvent(ctx context.Context, event *domain.JobEvent) {
	if s.events == nil || event == nil {
		return
	}
	if err := s.events.AddJobEvent(ctx, event); err != nil {
		s.logf("worker: append job event %s: %v", event.ID, err)
	}
}

func mergePayload(base, additional map[string]any) map[string]any {
	if base == nil && additional == nil {
		return nil
	}
	result := map[string]any{}
	for k, v := range base {
		result[k] = v
	}
	for k, v := range additional {
		result[k] = v
	}
	return result
}

func ensureEvent(event *domain.JobEvent, job domain.ProcessingJob, status domain.JobStatus, eventType, message string) *domain.JobEvent {
	if event == nil {
		event = &domain.JobEvent{}
	}
	if event.JobID == "" {
		event.JobID = job.TaskID
	}
	if event.Stage == "" {
		event.Stage = job.Stage
	}
	if event.Status == "" {
		event.Status = status
	}
	if event.EventType == "" {
		event.EventType = eventType
	}
	if event.Message == "" {
		event.Message = message
	}
	return event
}
