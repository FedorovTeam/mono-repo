package worker

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"preprocess-service/internal/domain"
	"preprocess-service/internal/usecase/ports"
)

type stubQueueRepo struct {
	jobs   chan domain.PreprocessJob
	doneCh chan string
	mu     sync.Mutex
	done   map[string]int
}

func newStubQueueRepo(jobs []domain.PreprocessJob) *stubQueueRepo {
	ch := make(chan domain.PreprocessJob, len(jobs))
	for _, job := range jobs {
		ch <- job
	}

	return &stubQueueRepo{
		jobs:   ch,
		doneCh: make(chan string, len(jobs)),
		done:   make(map[string]int),
	}
}

func (r *stubQueueRepo) NextJobForProcessing(ctx context.Context) (domain.PreprocessJob, bool, error) {
	select {
	case job := <-r.jobs:
		job.Attempts++
		return job, true, nil
	case <-ctx.Done():
		return domain.PreprocessJob{}, false, ctx.Err()
	default:
		return domain.PreprocessJob{}, false, nil
	}
}

func (r *stubQueueRepo) MarkDone(_ context.Context, job domain.PreprocessJob) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.done[job.ID]++
	r.doneCh <- job.ID
	return nil
}

func (r *stubQueueRepo) MarkFailed(_ context.Context, job domain.PreprocessJob, _ ports.FailureUpdate) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.done[job.ID]++
	r.doneCh <- job.ID
	return nil
}

type noopDownloader struct{}

func (noopDownloader) Download(_ context.Context, _, _, _ string, destinationPath string) error {
	return writeFile(destinationPath, []byte("content"))
}

type noopUploader struct {
	mu    sync.Mutex
	paths []string
}

func (u *noopUploader) Upload(_ context.Context, _, _, path, _ string) error {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.paths = append(u.paths, path)
	return nil
}

type noopConverter struct{}

func (noopConverter) Convert(_ context.Context, sourcePath, outDir string) error {
	return copySourceToOutput(sourcePath, outDir)
}

type noopCallback struct{}

func (noopCallback) Send(_ context.Context, _ string, _ ports.CallbackPayload) (bool, error) {
	return false, nil
}

func TestWorkerProcessesJobsConcurrently(t *testing.T) {
	t.Helper()

	now := time.Now().UTC()
	jobs := []domain.PreprocessJob{
		{ID: "job-1", DocumentID: "doc-1", SourceBucket: "src", SourceKey: "a.txt", SourceETag: "etag-1", TargetBucket: "t", TargetKey: "out/a.docx", Status: domain.StatusPending, MaxAttempts: domain.DefaultMaxAttempts, NextRunAt: now},
		{ID: "job-2", DocumentID: "doc-2", SourceBucket: "src", SourceKey: "b.txt", SourceETag: "etag-2", TargetBucket: "t", TargetKey: "out/b.docx", Status: domain.StatusPending, MaxAttempts: domain.DefaultMaxAttempts, NextRunAt: now},
		{ID: "job-3", DocumentID: "doc-3", SourceBucket: "src", SourceKey: "c.txt", SourceETag: "etag-3", TargetBucket: "t", TargetKey: "out/c.docx", Status: domain.StatusPending, MaxAttempts: domain.DefaultMaxAttempts, NextRunAt: now},
	}

	repo := newStubQueueRepo(jobs)
	uploader := &noopUploader{}

	w, err := New(Dependencies{
		Repo:       repo,
		Downloader: noopDownloader{},
		Uploader:   uploader,
		Converter:  noopConverter{},
		Callback:   noopCallback{},
	}, Config{
		WorkingDir:      t.TempDir(),
		OutputDir:       t.TempDir(),
		TargetBucket:    "t",
		TargetMediaType: "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
		Concurrency:     2,
		PollInterval:    5 * time.Millisecond,
		Backoff:         5 * time.Millisecond,
		ConvertTimeout:  time.Second,
	}, nil)
	if err != nil {
		t.Fatalf("init worker: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- w.Run(ctx)
	}()

	for i := 0; i < len(jobs); i++ {
		select {
		case <-repo.doneCh:
		case <-time.After(time.Second):
			t.Fatal("timeout waiting for jobs to complete")
		}
	}

	cancel()

	if err := <-errCh; err != nil && err != context.Canceled {
		t.Fatalf("worker returned error: %v", err)
	}

	if len(uploader.paths) != len(jobs) {
		t.Fatalf("expected %d uploads, got %d", len(jobs), len(uploader.paths))
	}
}

// Helpers to keep test dependencies lightweight.
func writeFile(path string, content []byte) error {
	if err := ensureDir(path); err != nil {
		return err
	}
	return os.WriteFile(path, content, 0o644)
}

func copySourceToOutput(sourcePath, outDir string) error {
	if err := ensureDir(outDir); err != nil {
		return err
	}
	content := []byte("converted")
	target := preprocessOutputPath(sourcePath, outDir)
	return os.WriteFile(target, content, 0o644)
}

func preprocessOutputPath(sourcePath, outDir string) string {
	_, name := filepath.Split(sourcePath)
	base := strings.TrimSuffix(name, filepath.Ext(name))
	return filepath.Join(outDir, base+".docx")
}

func ensureDir(path string) error {
	dir := filepath.Dir(path)
	return os.MkdirAll(dir, 0o755)
}
