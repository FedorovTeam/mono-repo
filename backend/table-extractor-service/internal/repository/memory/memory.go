package memory

import (
	"context"
	"sync"

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

func (r *InMemoryRepository) Create(_ context.Context, job domain.TableExtractionJob) (domain.TableExtractionJob, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.data[job.ID] = job
	r.byDoc[job.DocumentID] = job.ID
	return job, nil
}

func (r *InMemoryRepository) GetByDocumentID(_ context.Context, documentID string) (domain.TableExtractionJob, bool, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	id, ok := r.byDoc[documentID]
	if !ok {
		return domain.TableExtractionJob{}, false, nil
	}
	job, ok := r.data[id]
	return job, ok, nil
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
