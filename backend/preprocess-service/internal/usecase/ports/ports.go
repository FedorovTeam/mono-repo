package ports

import (
	"context"
	"errors"
	"time"

	"preprocess-service/internal/domain"
)

var (
	ErrObjectNotFound     = errors.New("storage object not found")
	ErrPreconditionFailed = errors.New("storage precondition failed")
	ErrBucketNotFound     = errors.New("storage bucket not found")
)

type Job struct {
	JobID         string
	SourceBucket  string
	SourceKey     string
	SourceETag    string
	TargetBucket  string
	TargetKey     string
	CallbackURL   string
	CorrelationID string
	Attempts      int
	MaxAttempts   int
}

type FailureUpdate struct {
	Status       domain.PreprocessStatus
	NextRunAt    time.Time
	Code         string
	Message      string
	Permanent    bool
	TargetBucket string
	TargetKey    string
	Details      map[string]any
}

type CallbackErrorInfo struct {
	Code    string
	Message string
}

type CallbackPayload struct {
	JobID        string                `json:"job_id"`
	Status       string                `json:"status"`
	TargetBucket string                `json:"target_bucket,omitempty"`
	TargetKey    string                `json:"target_key,omitempty"`
	Error        *CallbackErrorPayload `json:"error,omitempty"`
}

type CallbackErrorPayload struct {
	Code    string `json:"code,omitempty"`
	Message string `json:"message,omitempty"`
}

type StorageDownloader interface {
	Download(ctx context.Context, bucket, key, etag, destinationPath string) error
}

type StorageUploader interface {
	Upload(ctx context.Context, bucket, key, path, contentType string) error
}

type DocumentConverter interface {
	Convert(ctx context.Context, sourcePath, outDir string) error
}

type CallbackSender interface {
	Send(ctx context.Context, url string, payload CallbackPayload) (temporary bool, err error)
}

type QueueRepository interface {
	NextJobForProcessing(ctx context.Context) (Job, bool, error)
	MarkDone(ctx context.Context, job Job, targetMediaType string) error
	MarkFailed(ctx context.Context, job Job, update FailureUpdate) error
}
