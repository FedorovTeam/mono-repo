package document

import (
	"context"
	"database/sql/driver"
	"errors"
	"math/rand"
	"testing"
	"time"

	"backend-document-service/internal/domain"
)

func TestProcessingReconciler_RetryTemporaryFailures(t *testing.T) {
	repo := &processingRepoStub{
		responses: []stubResponse{
			{err: errors.New("driver: bad connection")},
			{err: context.DeadlineExceeded},
			{jobs: []domain.ProcessingJob{{TaskID: "task-1", Stage: domain.JobStageConvertToMarkdown}}},
			{jobs: []domain.ProcessingJob{}},
		},
	}

	reconciler := NewProcessingReconciler(repo, 1, time.Second, 0)
	reconciler.retryAttempts = 3
	reconciler.retryBackoff = time.Millisecond
	reconciler.maxBackoff = time.Millisecond
	reconciler.sleep = func(context.Context, time.Duration) {}
	reconciler.now = func() time.Time { return time.Unix(0, 0) }

	if err := reconciler.reconcile(context.Background(), time.Second); err != nil {
		t.Fatalf("unexpected reconcile error: %v", err)
	}

	if repo.calls != len(repo.responses) {
		t.Fatalf("expected %d calls to repo, got %d", len(repo.responses), repo.calls)
	}

	if !repo.finished {
		t.Fatalf("expected reconciler to complete stale job requeue after transient errors")
	}
}

type stubResponse struct {
	jobs []domain.ProcessingJob
	err  error
}

type processingRepoStub struct {
	responses []stubResponse
	calls     int
	finished  bool
}

func (s *processingRepoStub) RequeueStaleProcessingJobs(ctx context.Context, staleBefore time.Time, limit int, nextRunAt time.Time, reason string) ([]domain.ProcessingJob, error) {
	_ = ctx
	_ = staleBefore
	_ = limit
	_ = nextRunAt
	_ = reason

	s.calls++
	if s.calls > len(s.responses) {
		s.finished = true
		return nil, nil
	}

	resp := s.responses[s.calls-1]
	if resp.err == nil && len(resp.jobs) == 0 {
		s.finished = true
	}

	return resp.jobs, resp.err
}

func TestProcessingReconciler_CooldownAfterMaxRetries(t *testing.T) {
	repo := &flakyProcessingRepo{failTimes: 3}

	reconciler := NewProcessingReconciler(repo, 1, time.Second, 0)
	reconciler.retryAttempts = 2
	reconciler.retryBackoff = time.Millisecond
	reconciler.maxBackoff = time.Millisecond
	reconciler.random = rand.New(rand.NewSource(1))
	reconciler.now = func() time.Time { return time.Unix(0, 0) }

	var sleeps []time.Duration
	reconciler.sleep = func(ctx context.Context, d time.Duration) {
		sleeps = append(sleeps, d)
	}

	err := reconciler.reconcile(context.Background(), 50*time.Millisecond)
	if !errors.Is(err, driver.ErrBadConn) {
		t.Fatalf("expected error after retries, got %v", err)
	}

	if repo.calls != 3 {
		t.Fatalf("expected 3 attempts before cooldown, got %d", repo.calls)
	}

	if len(sleeps) != 3 {
		t.Fatalf("expected retries with cooldown, got %d sleeps", len(sleeps))
	}

	if sleeps[2] < 50*time.Millisecond {
		t.Fatalf("expected cooldown to be at least the interval, got %s", sleeps[2])
	}
}

type flakyProcessingRepo struct {
	failTimes int
	calls     int
	recovered bool
	delivered bool
	finished  bool
}

func (f *flakyProcessingRepo) RequeueStaleProcessingJobs(context.Context, time.Time, int, time.Time, string) ([]domain.ProcessingJob, error) {
	f.calls++

	if !f.recovered {
		if f.calls <= f.failTimes {
			return nil, driver.ErrBadConn
		}
		return nil, errors.New("connection still down")
	}

	if !f.delivered {
		f.delivered = true
		return []domain.ProcessingJob{{TaskID: "task-1", Stage: domain.JobStageConvertToMarkdown}}, nil
	}

	f.finished = true
	return []domain.ProcessingJob{}, nil
}
