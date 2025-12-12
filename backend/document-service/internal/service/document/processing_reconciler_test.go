package document

import (
	"context"
	"errors"
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

	reconciler.reconcile(context.Background())

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
