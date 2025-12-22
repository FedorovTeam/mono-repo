package ports

import (
	"context"
	"errors"
	"fmt"
	"time"
)

var (
	ErrObjectNotFound     = errors.New("storage object not found")
	ErrPreconditionFailed = errors.New("storage precondition failed")
	ErrBucketNotFound     = errors.New("storage bucket not found")
	ErrInvalidObjectKey   = errors.New("invalid object key")
)

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
	Convert(ctx context.Context, sourcePath, outDir string) (ConvertResult, error)
}

type CallbackSender interface {
	Send(ctx context.Context, url string, payload CallbackPayload, attempt int) (temporary bool, err error)
}

type WorkspaceManager interface {
	Prepare(jobID string) (Workspace, error)
}

type Workspace interface {
	SourcePath(sourceKey string) (string, error)
	OutputPath(sourceKey string) (string, error)
	OutputDir() string
	EnsureExists(path string) error
	Cleanup() error
}

type ConvertResult struct {
	ExitCode int
	Output   string
	Stdout   string
	Stderr   string
	Duration time.Duration
}

func (r ConvertResult) Details(prefix string) map[string]string {
	if prefix == "" {
		prefix = "lo"
	}

	details := map[string]string{
		prefix + "_exit_code":       fmt.Sprintf("%d", r.ExitCode),
		prefix + "_duration_ms":     fmt.Sprintf("%d", r.Duration.Milliseconds()),
		prefix + "_duration_string": r.Duration.String(),
	}

	if r.Output != "" {
		details[prefix+"_output"] = r.Output
	}

	return details
}
