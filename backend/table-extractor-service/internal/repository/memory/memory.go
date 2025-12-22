package memory

import (
	"context"
	"sort"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/mono-repo/table-extractor-service/internal/domain"
	"github.com/mono-repo/table-extractor-service/internal/repository"
)

type InMemoryRepository struct {
	mu    sync.RWMutex
	data  map[uuid.UUID]domain.TableExtractionJob
	byDoc map[string]uuid.UUID
}

func NewInMemoryRepository() repository.TableExtractionRepository {
	return &InMemoryRepository{
		data:  make(map[uuid.UUID]domain.TableExtractionJob),
		byDoc: make(map[string]uuid.UUID),
	}
}

func (r *InMemoryRepository) UpsertOrGetActive(_ context.Context, job domain.TableExtractionJob, queueLimit int) (domain.TableExtractionJob, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if err := job.Validate(); err != nil {
		return domain.TableExtractionJob{}, false, err
	}

	if existingID, ok := r.byDoc[job.DocumentID]; ok {
		existing := r.data[existingID]
		if existing.Status == domain.StatusPending || existing.Status == domain.StatusProcessing {
			return existing, true, nil
		}
	}

	if queueLimit > 0 {
		active := 0
		for _, item := range r.data {
			if item.Status == domain.StatusPending || item.Status == domain.StatusProcessing {
				active++
			}
		}
		if active >= queueLimit {
			return domain.TableExtractionJob{}, false, repository.ErrQueueLimitReached
		}
	}

	r.data[job.ID] = job
	r.byDoc[job.DocumentID] = job.ID
	return job, false, nil
}

func (r *InMemoryRepository) AcquirePendingBatch(ctx context.Context, limit int, now time.Time) ([]domain.TableExtractionJob, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	return r.selectPendingBatch(limit, now)
}

func (r *InMemoryRepository) SelectPendingBatchForUpdate(_ context.Context, limit int, now time.Time) ([]domain.TableExtractionJob, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	return r.selectPendingBatch(limit, now)
}

func (r *InMemoryRepository) ListProcessingBefore(_ context.Context, updatedBefore time.Time) ([]domain.TableExtractionJob, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	result := make([]domain.TableExtractionJob, 0)
	for _, job := range r.data {
		if job.Status != domain.StatusProcessing {
			continue
		}
		if !job.UpdatedAt.Before(updatedBefore) && !job.UpdatedAt.Equal(updatedBefore) {
			continue
		}
		result = append(result, job)
	}
	return result, nil
}

func (r *InMemoryRepository) Cleanup(_ context.Context, finishedBefore time.Time) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	removed := 0
	for id, job := range r.data {
		if job.FinishedAt == nil {
			continue
		}
		if job.Status != domain.StatusDone && job.Status != domain.StatusFailed {
			continue
		}
		if job.FinishedAt.Before(finishedBefore) {
			delete(r.data, id)
			delete(r.byDoc, job.DocumentID)
			removed++
		}
	}

	return removed, nil
}

func (r *InMemoryRepository) RetryFailed(_ context.Context, maxAttempts int, retryDelay time.Duration, now time.Time) ([]domain.TableExtractionJob, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	updated := make([]domain.TableExtractionJob, 0)
	for id, job := range r.data {
		if job.Status != domain.StatusFailed && job.Status != domain.StatusFailedRetryable {
			continue
		}
		if maxAttempts > 0 && job.Attempts >= maxAttempts {
			continue
		}

		aggregate := domain.NewTableExtractionJobAggregate(job)
		updatedAgg, _, err := aggregate.Retry(now, retryDelay)
		if err != nil {
			return updated, err
		}
		job = updatedAgg.Job()
		r.data[id] = job
		updated = append(updated, job)
	}

	return updated, nil
}

func (r *InMemoryRepository) Save(_ context.Context, job domain.TableExtractionJob) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if err := job.Validate(); err != nil {
		return err
	}
	r.data[job.ID] = job
	r.byDoc[job.DocumentID] = job.ID
	return nil
}

func (r *InMemoryRepository) selectPendingBatch(limit int, now time.Time) ([]domain.TableExtractionJob, error) {
	if limit <= 0 {
		return nil, nil
	}

	candidates := make([]domain.TableExtractionJob, 0, len(r.data))
	for _, job := range r.data {
		if job.Status != domain.StatusPending {
			continue
		}
		if job.NextRunAt != nil && job.NextRunAt.After(now) {
			continue
		}
		candidates = append(candidates, job)
	}

	sort.Slice(candidates, func(i, j int) bool {
		left := candidates[i].NextRunAt
		right := candidates[j].NextRunAt
		if left == nil && right == nil {
			return candidates[i].CreatedAt.Before(candidates[j].CreatedAt)
		}
		if left == nil {
			return true
		}
		if right == nil {
			return false
		}
		if left.Equal(*right) {
			return candidates[i].CreatedAt.Before(candidates[j].CreatedAt)
		}
		return left.Before(*right)
	})

	if len(candidates) == 0 {
		return nil, nil
	}

	if len(candidates) > limit {
		candidates = candidates[:limit]
	}

	result := make([]domain.TableExtractionJob, 0, len(candidates))
	for _, job := range candidates {
		result = append(result, job)
	}

	return result, nil
}

func (r *InMemoryRepository) GetByID(_ context.Context, id uuid.UUID) (domain.TableExtractionJob, bool, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	job, ok := r.data[id]
	return job, ok, nil
}

func (r *InMemoryRepository) CountActive(_ context.Context) (int, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	total := 0
	for _, job := range r.data {
		if job.Status == domain.StatusPending || job.Status == domain.StatusProcessing {
			total++
		}
	}
	return total, nil
}
