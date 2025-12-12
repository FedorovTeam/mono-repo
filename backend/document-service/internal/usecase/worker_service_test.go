package usecase

import (
	"context"
	"testing"
	"time"

	"backend-document-service/internal/domain"
)

type fakeJobRepo struct {
	jobs        []domain.ProcessingJob
	lastUpdate  domain.JobStatus
	lastPayload map[string]any
	lastEvent   *domain.JobEvent
	lastNextRun *time.Time
}

func (f *fakeJobRepo) ActiveJob(context.Context, string) (*domain.ProcessingJob, error) {
	return nil, nil
}
func (f *fakeJobRepo) ListFinishedJobs(context.Context, int, time.Duration) ([]domain.ProcessingJob, error) {
	return nil, nil
}
func (f *fakeJobRepo) AcquirePendingJobs(ctx context.Context, stage domain.JobStage, limit int) ([]domain.ProcessingJob, error) {
	var result []domain.ProcessingJob
	for _, job := range f.jobs {
		if job.Stage != stage {
			continue
		}
		result = append(result, job)
		if limit > 0 && len(result) >= limit {
			break
		}
	}
	return result, nil
}
func (f *fakeJobRepo) RequeueStaleProcessingJobs(context.Context, time.Time, int, time.Time, string) ([]domain.ProcessingJob, error) {
	return nil, nil
}
func (f *fakeJobRepo) CreateProcessingJob(context.Context, *domain.ProcessingJob, *domain.JobEvent) (*domain.ProcessingJob, error) {
	return nil, nil
}
func (f *fakeJobRepo) UpdateProcessingJob(ctx context.Context, taskID string, status domain.JobStatus, nextRunAt *time.Time, _ bool, payload map[string]any, event *domain.JobEvent) (*domain.ProcessingJob, error) {
	_ = ctx
	f.lastUpdate = status
	f.lastPayload = payload
	f.lastEvent = event
	f.lastNextRun = nextRunAt
	return &domain.ProcessingJob{TaskID: taskID, Status: status, Payload: payload}, nil
}

func TestWorkerServiceUpdatesJobsWithHandlerResult(t *testing.T) {
	repo := &fakeJobRepo{
		jobs: []domain.ProcessingJob{{
			TaskID:  "task-1",
			Stage:   domain.JobStageStoreOriginal,
			Payload: map[string]any{"foo": "bar"},
		}},
	}
	hooksCalled := 0
	service := NewWorkerService(repo, 1, WorkerHooks{Batch: func(context.Context, int) { hooksCalled++ }})

	handler := func(context.Context, domain.ProcessingJob) JobResult {
		return JobResult{
			Status:  domain.JobStatusCompleted,
			Payload: map[string]any{"foo": "updated", "bar": "baz"},
			Event:   &domain.JobEvent{EventType: "completed"},
		}
	}

	service.Process(context.Background(), domain.JobStageStoreOriginal, handler)

	if repo.lastUpdate != domain.JobStatusCompleted {
		t.Fatalf("expected completed status, got %s", repo.lastUpdate)
	}
	if repo.lastEvent == nil || repo.lastEvent.EventType != "completed" || repo.lastEvent.JobID != "task-1" {
		t.Fatalf("unexpected event saved: %#v", repo.lastEvent)
	}
	if repo.lastPayload["foo"] != "updated" || repo.lastPayload["bar"] != "baz" {
		t.Fatalf("unexpected payload: %#v", repo.lastPayload)
	}
	if hooksCalled == 0 {
		t.Fatalf("expected batch hook to be called")
	}
}

func TestWorkerServiceRespectsRetryAfter(t *testing.T) {
	repo := &fakeJobRepo{jobs: []domain.ProcessingJob{{TaskID: "task-2", Stage: domain.JobStagePublishToGitHub}}}
	retry := 2 * time.Second
	service := NewWorkerService(repo, 1, WorkerHooks{})

	handler := func(context.Context, domain.ProcessingJob) JobResult {
		return JobResult{Status: domain.JobStatusFailed, RetryAfter: retry}
	}

	start := time.Now()
	service.Process(context.Background(), domain.JobStagePublishToGitHub, handler)

	if repo.lastUpdate != domain.JobStatusFailed {
		t.Fatalf("expected failed status, got %s", repo.lastUpdate)
	}
	if repo.lastNextRun == nil {
		t.Fatalf("expected next run to be scheduled")
	}
	if repo.lastNextRun.Before(start.Add(retry)) {
		t.Fatalf("expected retry to be at least %s from start, got %s", retry, repo.lastNextRun.Sub(start))
	}
}
