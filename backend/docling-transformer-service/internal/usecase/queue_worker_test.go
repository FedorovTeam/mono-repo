package usecase

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	transformations "github.com/mono-repo/docling-transformer-service/internal/domain/transformations"
	repo "github.com/mono-repo/docling-transformer-service/internal/repository"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

type fakeQueueRepository struct {
	mu      sync.Mutex
	waiting []*transformations.TransformationTask
	pending []*transformations.TransformationTask
}

func (f *fakeQueueRepository) Enqueue(context.Context, transformations.EnqueueParams) (uuid.UUID, error) {
	return uuid.Nil, errors.New("not implemented")
}

func (f *fakeQueueRepository) AcquireNext(context.Context) (*transformations.TransformationTask, error) {
	return nil, errors.New("not implemented")
}

func (f *fakeQueueRepository) AcquireBatch(_ context.Context, limit int) ([]*transformations.TransformationTask, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if limit <= 0 {
		limit = 1
	}

	if len(f.waiting) > 0 {
		task := f.waiting[0]
		f.waiting = f.waiting[1:]
		task.Status = transformations.StatusProcessing
		return []*transformations.TransformationTask{task}, nil
	}

	if len(f.pending) == 0 {
		return nil, nil
	}

	if limit > len(f.pending) {
		limit = len(f.pending)
	}

	tasks := make([]*transformations.TransformationTask, limit)
	copy(tasks, f.pending[:limit])
	f.pending = f.pending[limit:]

	for _, task := range tasks {
		task.Status = transformations.StatusProcessing
	}

	return tasks, nil
}

func (f *fakeQueueRepository) GetLatestByDocument(context.Context, string) (*transformations.TransformationTask, error) {
	return nil, errors.New("not implemented")
}

func (f *fakeQueueRepository) GetByID(context.Context, uuid.UUID) (*transformations.TransformationTask, error) {
	return nil, errors.New("not implemented")
}

func (f *fakeQueueRepository) MarkDone(context.Context, uuid.UUID, string, string) error {
	return nil
}

func (f *fakeQueueRepository) MarkFailed(context.Context, uuid.UUID, string) error {
	return nil
}

func (f *fakeQueueRepository) UpdateDoclingInfo(context.Context, uuid.UUID, string, string, int) error {
	return nil
}

func (f *fakeQueueRepository) MarkDoclingWaiting(context.Context, uuid.UUID, string, string, int, time.Time) error {
	return nil
}

func (f *fakeQueueRepository) RequeueDoclingWaiting(context.Context, uuid.UUID, *time.Time) error {
	return nil
}

func (f *fakeQueueRepository) PrepareOverlayOCRPhase(context.Context, uuid.UUID, string, string) error {
	return nil
}

func (f *fakeQueueRepository) ClearOverlayState(context.Context, uuid.UUID) error { return nil }

func (f *fakeQueueRepository) ActivateWaiting(context.Context, time.Time, time.Duration) (int, int, error) {
	return 0, 0, nil
}

func (f *fakeQueueRepository) RestoreProcessing(context.Context) (int, error) {
	return 0, nil
}

func (f *fakeQueueRepository) RestoreStuckProcessing(context.Context, time.Duration, int, int) (int, int, error) {
	return 0, 0, nil
}

func (f *fakeQueueRepository) RetryFailed(context.Context, time.Time, int) (int, int, error) {
	return 0, 0, nil
}

func (f *fakeQueueRepository) CleanupFinished(context.Context, time.Duration) (int, error) {
	return 0, nil
}

func (f *fakeQueueRepository) CountByStatus(context.Context) (int, int, int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	return len(f.pending), 0, len(f.waiting), nil
}

type recordingProcessor struct {
	mu        sync.Mutex
	states    map[uuid.UUID]TaskState
	processed []uuid.UUID
}

func (p *recordingProcessor) ProcessTask(_ context.Context, task *transformations.TransformationTask) (TaskState, error) {
	p.mu.Lock()
	p.processed = append(p.processed, task.ID)
	p.mu.Unlock()

	if state, ok := p.states[task.ID]; ok {
		return state, nil
	}

	return StateCompleted, nil
}

func (p *recordingProcessor) processedInOrder(expected []uuid.UUID) bool {
	p.mu.Lock()
	defer p.mu.Unlock()

	if len(p.processed) < len(expected) {
		return false
	}

	for idx, id := range expected {
		if p.processed[idx] != id {
			return false
		}
	}

	return true
}

func (p *recordingProcessor) processedIDs() []uuid.UUID {
	p.mu.Lock()
	defer p.mu.Unlock()

	result := make([]uuid.UUID, len(p.processed))
	copy(result, p.processed)
	return result
}

func TestQueueWorkerPrefersWaitingDoclingTasks(t *testing.T) {
	waitingTask := &transformations.TransformationTask{ID: uuid.New(), Status: transformations.StatusDocling, CorrelationID: "wait"}
	pendingTask := &transformations.TransformationTask{ID: uuid.New(), Status: transformations.StatusPending, CorrelationID: "pend"}

	repo := &fakeQueueRepository{
		waiting: []*transformations.TransformationTask{waitingTask},
		pending: []*transformations.TransformationTask{pendingTask},
	}

	processor := &recordingProcessor{
		states: map[uuid.UUID]TaskState{
			waitingTask.ID: StateWaitingDocling,
			pendingTask.ID: StateCompleted,
		},
	}

	worker := NewQueueWorker(repo, processor, nil, zap.NewNop(), QueueWorkerConfig{
		Workers:      1,
		PollInterval: 10 * time.Millisecond,
		BatchSize:    5,
		RetryBackoff: time.Millisecond,
	})

	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	worker.Start(ctx, &wg)

	require.Eventually(t, func() bool {
		return processor.processedInOrder([]uuid.UUID{waitingTask.ID, pendingTask.ID})
	}, time.Second, 10*time.Millisecond)

	cancel()
	wg.Wait()

	require.Equal(t, []uuid.UUID{waitingTask.ID, pendingTask.ID}, processor.processedIDs())
}

var _ repo.QueueRepository = (*fakeQueueRepository)(nil)
