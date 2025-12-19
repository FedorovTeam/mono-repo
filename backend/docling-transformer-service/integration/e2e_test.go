//go:build integration
// +build integration

package integration

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/mono-repo/docling-transformer-service/internal/domain"
	transformations "github.com/mono-repo/docling-transformer-service/internal/domain/transformations"
	repo "github.com/mono-repo/docling-transformer-service/internal/repository"
	"github.com/mono-repo/docling-transformer-service/internal/usecase"
	"go.uber.org/zap"
)

type stubQueueRepo struct {
	enqueueCalled []transformations.EnqueueParams
	enqueueErr    error
	countErr      error

	acquireTasks []*transformations.TransformationTask
	markDoneIDs  []uuid.UUID
	markFailedID []uuid.UUID
	markMutex    sync.Mutex
	pending      int
	processing   int
	waiting      int
	restoreCalls int
	cleanupCalls int
	currentTask  *transformations.TransformationTask
}

func (s *stubQueueRepo) Enqueue(_ context.Context, params transformations.EnqueueParams) (uuid.UUID, error) {
	s.enqueueCalled = append(s.enqueueCalled, params)
	if s.enqueueErr != nil {
		return uuid.Nil, s.enqueueErr
	}
	id := uuid.New()
	return id, nil
}

func (s *stubQueueRepo) AcquireNext(context.Context) (*transformations.TransformationTask, error) {
	if len(s.acquireTasks) == 0 {
		return nil, nil
	}

	task := s.acquireTasks[0]
	s.acquireTasks = s.acquireTasks[1:]
	s.currentTask = task
	return task, nil
}

func (s *stubQueueRepo) AcquireBatch(_ context.Context, limit int) ([]*transformations.TransformationTask, error) {
	if len(s.acquireTasks) == 0 {
		return nil, nil
	}

	if limit <= 0 || limit > len(s.acquireTasks) {
		limit = 1
		if len(s.acquireTasks) < 1 {
			return nil, nil
		}
	}

	tasks := make([]*transformations.TransformationTask, limit)
	copy(tasks, s.acquireTasks[:limit])
	s.acquireTasks = s.acquireTasks[limit:]
	s.currentTask = tasks[len(tasks)-1]
	return tasks, nil
}

func (s *stubQueueRepo) AcquireSubmitLock(context.Context) (repo.SubmitLock, error) {
	return stubSubmitLock{}, nil
}

func (s *stubQueueRepo) GetByID(_ context.Context, id uuid.UUID) (*transformations.TransformationTask, error) {
	if s.currentTask != nil && s.currentTask.ID == id {
		return s.currentTask, nil
	}
	for _, task := range s.acquireTasks {
		if task != nil && task.ID == id {
			return task, nil
		}
	}
	return nil, nil
}

func (s *stubQueueRepo) GetLatestByDocument(context.Context, string) (*transformations.TransformationTask, error) {
	return s.currentTask, nil
}

func (s *stubQueueRepo) MarkDone(_ context.Context, id uuid.UUID, _, _ string) error {
	s.markMutex.Lock()
	defer s.markMutex.Unlock()
	s.markDoneIDs = append(s.markDoneIDs, id)
	return nil
}

func (s *stubQueueRepo) MarkFailed(_ context.Context, id uuid.UUID, _ string) error {
	s.markMutex.Lock()
	defer s.markMutex.Unlock()
	s.markFailedID = append(s.markFailedID, id)
	return nil
}

func (s *stubQueueRepo) UpdateDoclingInfo(context.Context, uuid.UUID, string, string, int) error {
	return nil
}

func (s *stubQueueRepo) MarkDoclingWaiting(_ context.Context, _ uuid.UUID, _, _ string, pollCount int, nextPoll time.Time) error {
	if s.currentTask != nil {
		clone := *s.currentTask
		clone.StartedAt = nil
		clone.Status = transformations.StatusDocling
		clone.DoclingPollCount = pollCount
		clone.NextPollAt = &nextPoll
		s.acquireTasks = append(s.acquireTasks, &clone)
	}

	return nil
}

func (s *stubQueueRepo) RequeueDoclingWaiting(_ context.Context, id uuid.UUID, nextPollAt *time.Time) error {
	if s.currentTask != nil && s.currentTask.ID == id {
		s.currentTask.DoclingTaskID = ""
		s.currentTask.DoclingJobID = ""
		s.currentTask.DoclingPollCount = 0
		s.currentTask.NextPollAt = nextPollAt
	}
	return nil
}

func (s *stubQueueRepo) ActivateWaiting(context.Context, time.Time, time.Duration) (int, int, error) {
	return 0, 0, nil
}

func (s *stubQueueRepo) RestoreProcessing(context.Context) (int, error) {
	s.restoreCalls++
	return 0, nil
}

func (s *stubQueueRepo) RestoreStuckProcessing(context.Context, time.Duration, int, int) (int, int, error) {
	s.restoreCalls++
	return 0, 0, nil
}

func (s *stubQueueRepo) RetryFailed(context.Context, time.Time, int) (int, int, error) {
	return 0, 0, nil
}

func (s *stubQueueRepo) CleanupFinished(context.Context, time.Duration) (int, error) {
	s.cleanupCalls++
	return 0, nil
}

func (s *stubQueueRepo) CountByStatus(context.Context) (int, int, int, error) {
	if s.countErr != nil {
		return 0, 0, 0, s.countErr
	}
	return s.pending, s.processing, s.waiting, nil
}

type stubSubmitLock struct{}

func (stubSubmitLock) Release(context.Context) error { return nil }

type stubStorage struct {
	downloadContent []byte
	downloadCalls   int
	uploaded        []byte
	lastBucket      string
	lastKey         string
	lastContentType string
}

func (s *stubStorage) Download(context.Context, string, string) ([]byte, error) {
	s.downloadCalls++
	return s.downloadContent, nil
}

func (s *stubStorage) Upload(_ context.Context, bucket, key, contentType string, content []byte) error {
	s.uploaded = content
	s.lastBucket = bucket
	s.lastKey = key
	s.lastContentType = contentType
	return nil
}

type stubConverter struct {
	result domain.ConvertResult
	err    error
	delay  time.Duration

	ids domain.DoclingIdentifiers

	inFlight    atomic.Int32
	maxInFlight atomic.Int32
}

func (s *stubConverter) Submit(context.Context, domain.ConvertRequest) (domain.DoclingIdentifiers, error) {
	current := s.inFlight.Add(1)
	if current > s.maxInFlight.Load() {
		s.maxInFlight.Store(current)
	}
	defer s.inFlight.Add(-1)

	if s.delay > 0 {
		time.Sleep(s.delay)
	}
	if s.err != nil {
		return domain.DoclingIdentifiers{}, s.err
	}
	if s.ids.TaskID == "" {
		s.ids = domain.DoclingIdentifiers{TaskID: "task-1", JobID: "job-1"}
	}
	return s.ids, nil
}

func (s *stubConverter) CheckStatus(context.Context, string) (domain.StatusCheckResult, error) {
	current := s.inFlight.Add(1)
	if current > s.maxInFlight.Load() {
		s.maxInFlight.Store(current)
	}
	defer s.inFlight.Add(-1)

	if s.delay > 0 {
		time.Sleep(s.delay)
	}
	if s.err != nil {
		return domain.StatusCheckResult{}, s.err
	}

	return domain.StatusCheckResult{Status: "success", Result: &s.result}, nil
}

var _ repo.QueueRepository = (*stubQueueRepo)(nil)
var _ domain.ObjectStorage = (*stubStorage)(nil)
var _ domain.Converter = (*stubConverter)(nil)

func TestSubmitConversionQueuesTask(t *testing.T) {
	repository := &stubQueueRepo{}
	uc := usecase.NewTransformerService(repository, &stubConverter{}, usecase.TransformationConfig{MarkdownBucket: "markdown-bucket"}, zap.NewNop(), nil)

	result, err := uc.SubmitConversion(context.Background(), usecase.SubmitConversionRequest{
		DocumentID:        "doc-1",
		OriginalBucket:    "orig",
		OriginalObjectKey: "path/file.docx",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Task == nil || result.Task.ID == uuid.Nil {
		t.Fatalf("queue id must be returned")
	}

	if len(repository.enqueueCalled) != 1 {
		t.Fatalf("expected single enqueue, got %d", len(repository.enqueueCalled))
	}

	enqueued := repository.enqueueCalled[0]
	if enqueued.MarkdownBucket != "markdown-bucket" {
		t.Fatalf("unexpected markdown bucket: %s", enqueued.MarkdownBucket)
	}
	if enqueued.MarkdownObjectKey == "" {
		t.Fatalf("markdown key should be auto generated")
	}
}

func TestSubmitConversionQueueLimit(t *testing.T) {
	repository := &stubQueueRepo{pending: 1, processing: 1}
	uc := usecase.NewTransformerService(repository, &stubConverter{}, usecase.TransformationConfig{MarkdownBucket: "md", QueueLimit: 2}, zap.NewNop(), nil)

	_, err := uc.SubmitConversion(context.Background(), usecase.SubmitConversionRequest{
		DocumentID:        "doc-1",
		OriginalBucket:    "orig",
		OriginalObjectKey: "file.docx",
	})
	if !errors.Is(err, usecase.ErrQueueLimitReached) {
		t.Fatalf("expected queue limit error, got %v", err)
	}
}

func TestProcessorProcessesTask(t *testing.T) {
	taskID := uuid.New()
	repository := &stubQueueRepo{
		acquireTasks: []*transformations.TransformationTask{{
			ID:                taskID,
			Status:            transformations.StatusDocling,
			DoclingTaskID:     "task-1",
			DoclingJobID:      "job-1",
			OriginalBucket:    "orig",
			OriginalObjectKey: "path/file.docx",
			MarkdownBucket:    "md",
			MarkdownObjectKey: "dest/file.md",
			FilenameExtension: "docx",
			CreatedAt:         time.Now(),
		}},
	}

	storage := &stubStorage{}
	conv := &stubConverter{result: domain.ConvertResult{Content: []byte("converted"), MediaType: "text/markdown"}}

	processor := usecase.NewProcessor(repository, conv, storage, zap.NewNop(), usecase.Config{
		MaxWorkers:           1,
		PollInterval:         5 * time.Millisecond,
		CleanupInterval:      20 * time.Millisecond,
		MarkdownBucket:       "md",
		FastOperationTimeout: 2 * time.Second,
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go processor.Run(ctx)

	assertWithin(t, time.Second, func() bool {
		repository.markMutex.Lock()
		defer repository.markMutex.Unlock()
		return len(repository.markDoneIDs) == 1
	})

	if string(storage.uploaded) != "converted" {
		t.Fatalf("expected markdown to be uploaded")
	}
	if storage.lastBucket != "md" || storage.lastKey != "dest/file.md" {
		t.Fatalf("unexpected upload destination %s/%s", storage.lastBucket, storage.lastKey)
	}
}

func assertWithin(t *testing.T, timeout time.Duration, condition func() bool) {
	t.Helper()

	deadline := time.After(timeout)
	for {
		select {
		case <-deadline:
			t.Fatalf("condition not met within %s", timeout)
		default:
			if condition() {
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
}
