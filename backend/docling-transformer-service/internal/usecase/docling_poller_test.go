package usecase

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/mono-repo/docling-transformer-service/internal/domain"
	transformations "github.com/mono-repo/docling-transformer-service/internal/domain/transformations"
	"github.com/mono-repo/docling-transformer-service/internal/infrastructure/converter"
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

func TestDoclingPollerUsesTaskIDForStatusAndResult(t *testing.T) {
	const (
		submitPath = "/v1/convert/file/async"
		statusPath = "/v1/status/poll/"
		resultPath = "/v1/result/"
	)

	var (
		taskStatusCalls int32
		jobStatusCalls  int32
		taskResultCalls int32
		jobResultCalls  int32
	)

	taskID := "docling-task-123"
	jobID := "docling-job-456"

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == submitPath:
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusAccepted)
			_ = json.NewEncoder(w).Encode(map[string]string{"task_id": taskID, "job_id": jobID})
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, statusPath):
			if strings.HasSuffix(r.URL.Path, taskID) {
				atomic.AddInt32(&taskStatusCalls, 1)
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]string{"task_status": "success"})

				return
			}

			if strings.HasSuffix(r.URL.Path, jobID) {
				atomic.AddInt32(&jobStatusCalls, 1)
				http.NotFound(w, r)

				return
			}

			t.Fatalf("unexpected status path: %s", r.URL.Path)
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, resultPath):
			if strings.HasSuffix(r.URL.Path, taskID) {
				atomic.AddInt32(&taskResultCalls, 1)
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]any{
					"status":   "success",
					"document": map[string]any{"md_content": "# done"},
				})

				return
			}

			if strings.HasSuffix(r.URL.Path, jobID) {
				atomic.AddInt32(&jobResultCalls, 1)
				http.NotFound(w, r)

				return
			}

			t.Fatalf("unexpected result path: %s", r.URL.Path)
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	repoStub := &pollingRepoStub{}
	workerMetrics := metrics.NewWorkerMetrics(zap.NewNop())
	conv := converter.NewDoclingConverter(converter.Config{Endpoint: server.URL}, zap.NewNop())
	poller := NewDoclingPoller(conv, repoStub, workerMetrics, zap.NewNop(), DoclingPollerConfig{InitialInterval: time.Second, MaxInterval: time.Second, MaxAttempts: 3})

	ids, err := conv.Submit(context.Background(), domain.ConvertRequest{
		FileName:    "document.docx",
		Content:     []byte("content"),
		ContentType: "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
	})
	if err != nil {
		t.Fatalf("unexpected submit error: %v", err)
	}

	if ids.TaskID != taskID || ids.JobID != jobID {
		t.Fatalf("unexpected ids from submit: %+v", ids)
	}

	task := &transformations.TransformationTask{
		ID:            uuid.New(),
		Status:        transformations.StatusDocling,
		DoclingTaskID: ids.TaskID,
		DoclingJobID:  ids.JobID,
		CreatedAt:     time.Now(),
	}

	result, pollErr := poller.Poll(context.Background(), task)
	if pollErr != nil {
		t.Fatalf("unexpected poll error: %v", pollErr)
	}

	if result == nil || string(result.Content) != "# done" {
		t.Fatalf("unexpected poll result: %+v", result)
	}

	if taskStatusCalls != 1 || taskResultCalls != 1 {
		t.Fatalf("expected single task-based status/result call, got status=%d result=%d", taskStatusCalls, taskResultCalls)
	}

	if jobStatusCalls != 0 || jobResultCalls != 0 {
		t.Fatalf("expected no job-based polling, got status=%d result=%d", jobStatusCalls, jobResultCalls)
	}
}

func TestDoclingPollerRequeuesAfterRepeatedStatusErrors(t *testing.T) {
	repoStub := &pollingRepoStub{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/v1/status/poll/"):
			w.Header().Set("Content-Type", "application/json")
			http.Error(w, "not found", http.StatusNotFound)
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	conv := converter.NewDoclingConverter(converter.Config{Endpoint: server.URL}, nil)
	config := DoclingPollerConfig{InitialInterval: 10 * time.Millisecond, MaxInterval: time.Second, BackoffFactor: 2, MaxAttempts: 5}
	poller := NewDoclingPoller(conv, repoStub, nil, nil, config)

	task := &transformations.TransformationTask{ID: uuid.New(), DoclingTaskID: "job-404", DoclingJobID: "job-404", CreatedAt: time.Now().Add(-time.Minute)}

	if _, err := poller.Poll(context.Background(), task); !errors.Is(err, ErrDoclingWaiting) {
		t.Fatalf("expected waiting error, got %v", err)
	}

	if repoStub.requeueCalls != 0 {
		t.Fatalf("expected no requeue after first error, got %d", repoStub.requeueCalls)
	}
	if repoStub.markWaitingCalls != 1 {
		t.Fatalf("expected mark waiting call, got %d", repoStub.markWaitingCalls)
	}

	result, err := poller.Poll(context.Background(), task)
	if !errors.Is(err, ErrDoclingWaiting) {
		t.Fatalf("expected waiting error, got %v", err)
	}
	if result != nil {
		t.Fatalf("expected no result on repeated error")
	}

	if repoStub.requeueCalls != 1 {
		t.Fatalf("expected single requeue after repeated errors, got %d", repoStub.requeueCalls)
	}
	if task.DoclingTaskID != "" || task.DoclingJobID != "" {
		t.Fatalf("expected docling identifiers to be cleared after requeue, got task=%s job=%s", task.DoclingTaskID, task.DoclingJobID)
	}
	if task.DoclingPollCount != 0 {
		t.Fatalf("expected poll count reset after requeue, got %d", task.DoclingPollCount)
	}
}

func TestDoclingPollerFailsOnDoclingFailureError(t *testing.T) {
	repoStub := &pollingRepoStub{}
	failureErr := &domain.DoclingFailureError{TaskID: "docling-task", Status: "failed", Details: "parser: boom"}
	poller := NewDoclingPoller(&failingStatusConverter{err: failureErr}, repoStub, metrics.NewWorkerMetrics(zap.NewNop()), zap.NewNop(), DoclingPollerConfig{InitialInterval: time.Second})

	task := &transformations.TransformationTask{
		ID:            uuid.New(),
		Status:        transformations.StatusDocling,
		DoclingTaskID: "docling-task",
		CreatedAt:     time.Now(),
	}

	_, err := poller.Poll(context.Background(), task)
	if err == nil {
		t.Fatal("expected docling failure error")
	}

	if errors.Is(err, ErrDoclingWaiting) {
		t.Fatalf("expected fatal error, got waiting: %v", err)
	}

	var doclingErr *domain.DoclingFailureError
	if !errors.As(err, &doclingErr) {
		t.Fatalf("expected docling failure error, got %v", err)
	}

	if doclingErr.Details != failureErr.Details {
		t.Fatalf("expected details %q, got %q", failureErr.Details, doclingErr.Details)
	}

	if repoStub.markWaitingCalls != 0 {
		t.Fatalf("expected no waiting transitions on fatal error, got %d", repoStub.markWaitingCalls)
	}

	if repoStub.requeueCalls != 0 {
		t.Fatalf("expected no requeue on fatal error, got %d", repoStub.requeueCalls)
	}
}

type failingStatusConverter struct {
	err error
}

func (f *failingStatusConverter) Submit(context.Context, domain.ConvertRequest) (domain.DoclingIdentifiers, error) {
	return domain.DoclingIdentifiers{}, nil
}

func (f *failingStatusConverter) CheckStatus(context.Context, string) (domain.StatusCheckResult, error) {
	return domain.StatusCheckResult{}, f.err
}
