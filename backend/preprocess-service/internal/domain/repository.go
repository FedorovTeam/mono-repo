package domain

import (
	"context"
	"time"
)

// PreprocessJobRepository определяет контракты работы с заданиями предобработки.
type PreprocessJobRepository interface {
	Create(ctx context.Context, job PreprocessJob) error
	FindBySource(ctx context.Context, bucket, key, etag string) (PreprocessJob, bool, error)
	Get(ctx context.Context, id string) (PreprocessJob, bool, error)
	RequeueStuck(ctx context.Context, convertTimeout, backoff time.Duration) error
	NextForProcessing(ctx context.Context) (PreprocessJob, bool, error)
	MarkDone(ctx context.Context, job PreprocessJob) error
	MarkFailed(ctx context.Context, job PreprocessJob, update FailureUpdate) error
	TryAcquireWorkerLock(ctx context.Context) (LockHandle, bool, error)
}
