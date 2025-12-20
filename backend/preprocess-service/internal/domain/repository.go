package domain

import "context"

type JobRepository interface {
	Save(ctx context.Context, job PreprocessJob) error
	FindBySource(ctx context.Context, bucket, key, etag string) (PreprocessJob, bool, error)
	Get(ctx context.Context, id string) (PreprocessJob, bool, error)
}
