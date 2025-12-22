package memory

import (
	"context"
	"fmt"
	"slices"
	"sync"
	"time"

	"preprocess-service/internal/domain"
)

type Repository struct {
	mu       sync.Mutex
	jobs     map[string]domain.PreprocessJob
	bySource map[string]string
	locked   bool
}

func NewRepository() *Repository {
	return &Repository{
		jobs:     make(map[string]domain.PreprocessJob),
		bySource: make(map[string]string),
	}
}

func (r *Repository) Create(_ context.Context, job domain.PreprocessJob) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.jobs[job.ID] = cloneJob(job)
	r.bySource[r.sourceKey(job.SourceBucket, job.SourceKey, job.SourceETag)] = job.ID

	return nil
}

func (r *Repository) FindBySource(_ context.Context, bucket, key, etag string) (domain.PreprocessJob, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	jobID, ok := r.bySource[r.sourceKey(bucket, key, etag)]
	if !ok {
		return domain.PreprocessJob{}, false, nil
	}

	job, ok := r.jobs[jobID]
	if !ok {
		return domain.PreprocessJob{}, false, fmt.Errorf("job %s not found for index", jobID)
	}

	return cloneJob(job), true, nil
}

func (r *Repository) Get(_ context.Context, id string) (domain.PreprocessJob, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	job, ok := r.jobs[id]
	if !ok {
		return domain.PreprocessJob{}, false, nil
	}

	return cloneJob(job), true, nil
}

func (r *Repository) NextForProcessing(_ context.Context) (domain.PreprocessJob, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	now := time.Now().UTC()
	candidates := make([]domain.PreprocessJob, 0, len(r.jobs))
	for _, job := range r.jobs {
		if (job.Status == domain.StatusPending || job.Status == domain.StatusFailed) && !job.NextRunAt.After(now) {
			candidates = append(candidates, job)
		}
	}

	if len(candidates) == 0 {
		return domain.PreprocessJob{}, false, nil
	}

	slices.SortFunc(candidates, func(a, b domain.PreprocessJob) int {
		switch {
		case a.NextRunAt.Before(b.NextRunAt):
			return -1
		case a.NextRunAt.After(b.NextRunAt):
			return 1
		default:
			return 0
		}
	})

	job := candidates[0]
	updated, err := job.MarkProcessing(now)
	if err != nil {
		return domain.PreprocessJob{}, false, err
	}
	r.jobs[updated.ID] = cloneJob(updated)

	return cloneJob(updated), true, nil
}

func (r *Repository) RequeueStuck(_ context.Context, convertTimeout, backoff time.Duration) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if convertTimeout < 0 {
		convertTimeout = 0
	}
	if backoff < 0 {
		backoff = 0
	}

	now := time.Now().UTC()
	staleStartedBefore := now.Add(-(convertTimeout + backoff))
	nextRun := now.Add(backoff)

	for id, job := range r.jobs {
		if job.Status != domain.StatusProcessing {
			continue
		}
		if job.StartedAt.IsZero() || !job.StartedAt.Before(staleStartedBefore) {
			continue
		}

		job.Status = domain.StatusFailed
		job.Attempts++
		job.NextRunAt = nextRun
		job.UpdatedAt = now

		r.jobs[id] = cloneJob(job)
	}

	return nil
}

func (r *Repository) MarkDone(_ context.Context, job domain.PreprocessJob) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.jobs[job.ID] = cloneJob(job)
	return nil
}

func (r *Repository) MarkFailed(_ context.Context, job domain.PreprocessJob, _ domain.FailureUpdate) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.jobs[job.ID] = cloneJob(job)
	return nil
}

func (r *Repository) TryAcquireWorkerLock(ctx context.Context) (domain.LockHandle, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.locked {
		return nil, false, nil
	}

	r.locked = true
	return memoryLockHandle{repo: r}, true, nil
}

type memoryLockHandle struct {
	repo *Repository
}

func (h memoryLockHandle) Release(context.Context) error {
	if h.repo == nil {
		return nil
	}

	h.repo.mu.Lock()
	defer h.repo.mu.Unlock()
	h.repo.locked = false

	return nil
}

func (r *Repository) sourceKey(bucket, key, etag string) string {
	return fmt.Sprintf("%s|%s|%s", bucket, key, etag)
}

func (r *Repository) UpdateStage(_ context.Context, jobID string, update domain.StageUpdate) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	job, ok := r.jobs[jobID]
	if !ok {
		return fmt.Errorf("job %s not found", jobID)
	}

	job.ProcessingStage = update.Stage
	if update.StageStartedAt != nil {
		job.StageStartedAt = update.StageStartedAt.UTC()
	}
	job.StageUpdatedAt = update.StageUpdatedAt.UTC()
	job.UpdatedAt = update.StageUpdatedAt.UTC()

	r.jobs[jobID] = cloneJob(job)

	return nil
}

func cloneJob(job domain.PreprocessJob) domain.PreprocessJob {
	if job.ErrorDetails == nil {
		job.ErrorDetails = map[string]string{}
		return job
	}

	details := make(map[string]string, len(job.ErrorDetails))
	for k, v := range job.ErrorDetails {
		details[k] = v
	}

	job.ErrorDetails = details

	return job
}
