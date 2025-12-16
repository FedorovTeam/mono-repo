package memory

import (
	"context"
	"fmt"
	"sync"

	"preprocess-service/internal/domain"
)

type JobRepository struct {
	mu       sync.RWMutex
	jobs     map[string]domain.PreprocessJob
	bySource map[string]string
}

func NewJobRepository() *JobRepository {
	return &JobRepository{
		jobs:     make(map[string]domain.PreprocessJob),
		bySource: make(map[string]string),
	}
}

func (r *JobRepository) Save(_ context.Context, job domain.PreprocessJob) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.jobs[job.ID] = cloneJob(job)
	r.bySource[r.sourceKey(job.SourceBucket, job.SourceKey, job.SourceETag)] = job.ID

	return nil
}

func (r *JobRepository) FindBySource(_ context.Context, bucket, key, etag string) (domain.PreprocessJob, bool, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

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

func (r *JobRepository) Get(_ context.Context, id string) (domain.PreprocessJob, bool, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	job, ok := r.jobs[id]
	if !ok {
		return domain.PreprocessJob{}, false, nil
	}

	return cloneJob(job), true, nil
}

func (r *JobRepository) sourceKey(bucket, key, etag string) string {
	return fmt.Sprintf("%s|%s|%s", bucket, key, etag)
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
