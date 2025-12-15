package usecase

import (
	"context"
	"errors"
	"testing"
	"time"

	"backend-document-service/internal/domain"
)

type jobUpdate struct {
	taskID  string
	status  domain.JobStatus
	nextRun *time.Time
	payload map[string]any
}

type fakeJobRepo struct {
	jobs    []domain.ProcessingJob
	updates []jobUpdate
	events  []domain.JobEvent
}

func (r *fakeJobRepo) CreateDocument(context.Context, *domain.Document, *domain.DocumentVersion, *domain.DocumentStatusLog) (*domain.Document, error) {
	return nil, errors.New("not implemented")
}

func (r *fakeJobRepo) GetDocument(context.Context, string) (*domain.Document, error) {
	return nil, errors.New("not implemented")
}
func (r *fakeJobRepo) ListDocuments(context.Context, int, int, *domain.DocumentStatus) ([]domain.Document, error) {
	return nil, errors.New("not implemented")
}
func (r *fakeJobRepo) UpdateStatus(context.Context, string, domain.DocumentStatus, string, string, *domain.DocumentVersion) (*domain.Document, error) {
	return nil, errors.New("not implemented")
}
func (r *fakeJobRepo) LastNonFailedStatus(context.Context, string) (domain.DocumentStatus, error) {
	return "", errors.New("not implemented")
}
func (r *fakeJobRepo) CountStatusLogsWithReasonPrefix(context.Context, string, domain.DocumentStatus, string) (int, error) {
	return 0, errors.New("not implemented")
}
func (r *fakeJobRepo) CreateProcessingJob(context.Context, *domain.ProcessingJob, *domain.JobEvent) (*domain.ProcessingJob, error) {
	return nil, errors.New("not implemented")
}
func (r *fakeJobRepo) ActiveJob(context.Context, string) (*domain.ProcessingJob, error) {
	return nil, nil
}

func (r *fakeJobRepo) AcquirePendingJobs(context.Context, domain.JobStage, int) ([]domain.ProcessingJob, error) {
	jobs := r.jobs
	r.jobs = nil
	return jobs, nil
}

func (r *fakeJobRepo) RequeueStaleProcessingJobs(context.Context, time.Time, int, time.Time, string) ([]domain.ProcessingJob, error) {
	return nil, nil
}

func (r *fakeJobRepo) UpdateProcessingJob(_ context.Context, taskID string, status domain.JobStatus, nextRunAt *time.Time, _ bool, payload map[string]any, _ *domain.JobEvent) (*domain.ProcessingJob, error) {
	r.updates = append(r.updates, jobUpdate{taskID: taskID, status: status, nextRun: nextRunAt, payload: payload})
	return &domain.ProcessingJob{TaskID: taskID, Status: status, Payload: payload}, nil
}

func (r *fakeJobRepo) AddJobEvent(_ context.Context, event *domain.JobEvent) error {
	if event != nil {
		r.events = append(r.events, *event)
	}
	return nil
}

func TestWorkerServiceCompletesJob(t *testing.T) {
	repo := &fakeJobRepo{jobs: []domain.ProcessingJob{{TaskID: "task-1", Stage: domain.JobStageStoreOriginal}}}
	svc := NewWorkerService(repo, repo, time.Second)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan struct{})
	go func() {
		svc.Run(ctx, domain.JobStageStoreOriginal, time.Hour, 10, func(_ context.Context, job domain.ProcessingJob) (JobResult, error) {
			return JobResult{Payload: map[string]any{"ok": true}, Event: &domain.JobEvent{EventType: "custom"}}, nil
		}, nil)
		close(done)
	}()

	waitForUpdates(t, repo, 1)
	cancel()
	<-done

	if len(repo.updates) != 1 {
		t.Fatalf("expected 1 update, got %d", len(repo.updates))
	}
	upd := repo.updates[0]
	if upd.status != domain.JobStatusCompleted {
		t.Fatalf("expected status completed, got %s", upd.status)
	}
	if val, ok := upd.payload["ok"]; !ok || val != true {
		t.Fatalf("payload not propagated: %v", upd.payload)
	}
	if len(repo.events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(repo.events))
	}
	if repo.events[0].EventType != "custom" || repo.events[0].JobID != "task-1" {
		t.Fatalf("unexpected event: %+v", repo.events[0])
	}
}

func TestWorkerServiceFailsJob(t *testing.T) {
	repo := &fakeJobRepo{jobs: []domain.ProcessingJob{{TaskID: "task-2", Stage: domain.JobStagePublishToGitHub}}}
	retryAt := time.Date(2024, 1, 1, 10, 0, 0, 0, time.UTC)

	svc := NewWorkerService(repo, repo, time.Minute)
	svc.SetClock(func() time.Time { return retryAt })

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go svc.Run(ctx, domain.JobStagePublishToGitHub, time.Hour, 5, func(_ context.Context, job domain.ProcessingJob) (JobResult, error) {
		next := retryAt.Add(2 * time.Minute)
		return JobResult{Payload: map[string]any{"attempt": 1}, NextRun: &next}, errors.New("network failed")
	}, nil)

	waitForUpdates(t, repo, 1)
	cancel()

	upd := repo.updates[0]
	if upd.status != domain.JobStatusFailed {
		t.Fatalf("expected failed status, got %s", upd.status)
	}
	if upd.nextRun == nil || !upd.nextRun.Equal(retryAt.Add(2*time.Minute)) {
		t.Fatalf("unexpected next run: %v", upd.nextRun)
	}
	if upd.payload["last_error"] != "network failed" {
		t.Fatalf("last_error not set in payload: %v", upd.payload)
	}
	if len(repo.events) != 1 || repo.events[0].Status != domain.JobStatusFailed {
		t.Fatalf("event not recorded: %+v", repo.events)
	}
}

func TestWorkerServicePostponesJob(t *testing.T) {
	repo := &fakeJobRepo{jobs: []domain.ProcessingJob{{TaskID: "task-3", Stage: domain.JobStageConvertToMarkdown}}}
	retryAt := time.Date(2024, 2, 2, 15, 0, 0, 0, time.UTC)

	svc := NewWorkerService(repo, repo, time.Minute)
	svc.SetClock(func() time.Time { return retryAt })

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go svc.Run(ctx, domain.JobStageConvertToMarkdown, time.Hour, 5, func(_ context.Context, job domain.ProcessingJob) (JobResult, error) {
		next := retryAt.Add(30 * time.Second)
		return JobResult{Payload: map[string]any{"queue_id": "queue-1"}, NextRun: &next, Status: domain.JobStatusPending}, nil
	}, nil)

	waitForUpdates(t, repo, 1)
	cancel()

	upd := repo.updates[0]
	if upd.status != domain.JobStatusPending {
		t.Fatalf("expected pending status, got %s", upd.status)
	}
	if upd.nextRun == nil || !upd.nextRun.Equal(retryAt.Add(30*time.Second)) {
		t.Fatalf("unexpected next run: %v", upd.nextRun)
	}
	if upd.payload["queue_id"] != "queue-1" {
		t.Fatalf("payload not merged: %v", upd.payload)
	}
	if len(repo.events) == 0 || repo.events[0].EventType != "postponed" || repo.events[0].Status != domain.JobStatusPending {
		t.Fatalf("postpone event not recorded: %+v", repo.events)
	}
}

func waitForUpdates(t *testing.T, repo *fakeJobRepo, expected int) {
	t.Helper()
	deadline := time.After(time.Second)
	for {
		if len(repo.updates) >= expected {
			return
		}
		select {
		case <-deadline:
			t.Fatalf("updates not reached %d, got %d", expected, len(repo.updates))
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}
}
