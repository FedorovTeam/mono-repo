package worker

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"preprocess-service/internal/usecase/ports"
)

type stubQueueRepo struct {
	jobs   chan ports.Job
	doneCh chan string
	mu     sync.Mutex
	done   map[string]int
}

func newStubQueueRepo(jobs []ports.Job) *stubQueueRepo {
	ch := make(chan ports.Job, len(jobs))
	for _, job := range jobs {
		ch <- job
	}

	return &stubQueueRepo{
		jobs:   ch,
		doneCh: make(chan string, len(jobs)),
		done:   make(map[string]int),
	}
}

func (r *stubQueueRepo) NextJobForProcessing(ctx context.Context) (ports.Job, bool, error) {
	select {
	case job := <-r.jobs:
		job.Attempts++
		return job, true, nil
	case <-ctx.Done():
		return ports.Job{}, false, ctx.Err()
	default:
		return ports.Job{}, false, nil
	}
}

func (r *stubQueueRepo) MarkDone(_ context.Context, job ports.Job, _ string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.done[job.JobID]++
	r.doneCh <- job.JobID
	return nil
}

func (r *stubQueueRepo) MarkFailed(_ context.Context, job ports.Job, _ ports.FailureUpdate) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.done[job.JobID]++
	r.doneCh <- job.JobID
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

	jobs := []ports.Job{
		{JobID: "job-1", SourceBucket: "src", SourceKey: "a.txt", TargetBucket: "t", TargetKey: "out/a.docx"},
		{JobID: "job-2", SourceBucket: "src", SourceKey: "b.txt", TargetBucket: "t", TargetKey: "out/b.docx"},
		{JobID: "job-3", SourceBucket: "src", SourceKey: "c.txt", TargetBucket: "t", TargetKey: "out/c.docx"},
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
