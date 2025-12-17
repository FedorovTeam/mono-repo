package usecase

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	transformations "github.com/mono-repo/docling-transformer-service/internal/domain/transformations"
	"github.com/mono-repo/docling-transformer-service/internal/usecase/metrics"
	"go.uber.org/zap"
)

func TestDoclingPollerRequeuesAfterWaitingLimit(t *testing.T) {
	repoStub := &pollingRepoStub{}
	conv := &converterWithTemporaryError{}
	workerMetrics := metrics.NewWorkerMetrics(zap.NewNop())
	poller := NewDoclingPoller(conv, repoStub, workerMetrics, zap.NewNop(), DoclingPollerConfig{MaxAttempts: 2, InitialInterval: time.Second, RequeueDelay: time.Minute, MaxRestarts: 2})

	task := &transformations.TransformationTask{
		ID:               uuid.New(),
		Status:           transformations.StatusProcessing,
		DoclingTaskID:    "task-1",
		DoclingJobID:     "job-1",
		DoclingPollCount: 1,
		RetryCount:       2,
	}

	result, err := poller.Poll(context.Background(), task)
	if err == nil || !errors.Is(err, ErrDoclingWaiting) {
		t.Fatalf("expected waiting error, got %v", err)
	}
	if result != nil {
		t.Fatalf("expected no result after requeue")
	}

	if repoStub.requeueCalls != 1 {
		t.Fatalf("expected requeue to be called once, got %d", repoStub.requeueCalls)
	}
	if repoStub.lastRequeueID != task.ID {
		t.Fatalf("expected requeue for task %s, got %s", task.ID, repoStub.lastRequeueID)
	}
	if task.DoclingTaskID != "" || task.DoclingJobID != "" || task.DoclingPollCount != 0 {
		t.Fatalf("expected docling data to be cleared, got task_id=%s job_id=%s polls=%d", task.DoclingTaskID, task.DoclingJobID, task.DoclingPollCount)
	}
	if task.NextPollAt == nil {
		t.Fatalf("expected next poll to be set after restart limit, got nil")
	}
	delay := time.Until(*task.NextPollAt)
	if delay < time.Minute-2*time.Second || delay > time.Minute+2*time.Second {
		t.Fatalf("expected next poll around 1m, got %s", delay)
	}
}
