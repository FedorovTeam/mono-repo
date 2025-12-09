package integration

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/mono-repo/docling-transformer-service/internal/converter"
	"github.com/mono-repo/docling-transformer-service/internal/repository"
	"github.com/mono-repo/docling-transformer-service/internal/server"
	"github.com/mono-repo/docling-transformer-service/internal/storage"
	"github.com/mono-repo/docling-transformer-service/internal/worker"
	transformerv1 "github.com/mono-repo/docling-transformer-service/proto/backend/transformer/v1"
	"go.uber.org/zap"
)

type stubQueueRepo struct {
	enqueueCalled []repository.EnqueueParams
	active        int
	enqueueErr    error
	countErr      error

	acquireTasks []*repository.TransformationTask
	markDoneIDs  []uuid.UUID
	markFailedID []uuid.UUID
	markMutex    sync.Mutex
}

func (s *stubQueueRepo) Enqueue(_ context.Context, params repository.EnqueueParams) (uuid.UUID, error) {
	s.enqueueCalled = append(s.enqueueCalled, params)
	if s.enqueueErr != nil {
		return uuid.Nil, s.enqueueErr
	}
	return uuid.New(), nil
}

func (s *stubQueueRepo) CountActive(context.Context) (int, error) {
	if s.countErr != nil {
		return 0, s.countErr
	}
	return s.active, nil
}

func (s *stubQueueRepo) AcquireNext(context.Context) (*repository.TransformationTask, error) {
	if len(s.acquireTasks) == 0 {
		return nil, nil
	}

	task := s.acquireTasks[0]
	s.acquireTasks = s.acquireTasks[1:]
	return task, nil
}

func (s *stubQueueRepo) MarkDone(_ context.Context, id uuid.UUID, _, _ string) error {
	s.markMutex.Lock()
	defer s.markMutex.Unlock()
	s.markDoneIDs = append(s.markDoneIDs, id)
	return nil
}

func (s *stubQueueRepo) MarkFailed(_ context.Context, id uuid.UUID, _ string) error {
	s.markMutex.Lock()
	defer s.markMutex.Unlock()
	s.markFailedID = append(s.markFailedID, id)
	return nil
}

type stubStorage struct {
	downloadContent []byte
	uploaded        []byte
}

func (s *stubStorage) Download(context.Context, string, string) ([]byte, error) {
	return s.downloadContent, nil
}

func (s *stubStorage) Upload(_ context.Context, _, _ string, _ string, content []byte) error {
	s.uploaded = content
	return nil
}

type stubConverter struct {
	result converter.DoclingConvertResult
	err    error
}

func (s *stubConverter) Convert(context.Context, converter.DoclingConvertRequest) (converter.DoclingConvertResult, error) {
	if s.err != nil {
		return converter.DoclingConvertResult{}, s.err
	}
	return s.result, nil
}

var _ repository.QueueRepository = (*stubQueueRepo)(nil)
var _ storage.ObjectStorage = (*stubStorage)(nil)
var _ converter.Converter = (*stubConverter)(nil)

func TestConvertDocumentQueuesTask(t *testing.T) {
	repo := &stubQueueRepo{active: 0}
	api := server.NewTransformerAPI(repo, "markdown-bucket", 1, zap.NewNop())

	resp, err := api.ConvertDocumentToMarkdown(context.Background(), &transformerv1.ConvertDocumentToMarkdownRequest{
		DocumentId:        "doc-1",
		OriginalBucket:    "orig",
		OriginalObjectKey: "path/file.docx",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if resp.GetQueueId() == "" {
		t.Fatalf("queue id must be returned")
	}

	if len(repo.enqueueCalled) != 1 {
		t.Fatalf("expected single enqueue, got %d", len(repo.enqueueCalled))
	}

	enqueued := repo.enqueueCalled[0]
	if enqueued.MarkdownBucket != "markdown-bucket" {
		t.Fatalf("unexpected markdown bucket: %s", enqueued.MarkdownBucket)
	}
	if enqueued.MarkdownObjectKey == "" {
		t.Fatalf("markdown key should be auto generated")
	}
}

func TestConvertDocumentResourceExhausted(t *testing.T) {
	repo := &stubQueueRepo{active: 2}
	api := server.NewTransformerAPI(repo, "md", 1, zap.NewNop())

	_, err := api.ConvertDocumentToMarkdown(context.Background(), &transformerv1.ConvertDocumentToMarkdownRequest{
		DocumentId:        "doc-1",
		OriginalBucket:    "orig",
		OriginalObjectKey: "file.docx",
	})
	if err == nil {
		t.Fatal("expected resource exhausted error")
	}
}

func TestWorkerProcessesTask(t *testing.T) {
	taskID := uuid.New()
	repo := &stubQueueRepo{
		acquireTasks: []*repository.TransformationTask{{
			ID:                taskID,
			OriginalBucket:    "orig",
			OriginalObjectKey: "path/file.docx",
			MarkdownBucket:    "md",
			MarkdownObjectKey: "dest/file.md",
			FilenameExtension: "docx",
		}},
	}

	storage := &stubStorage{downloadContent: []byte("source")}
	conv := &stubConverter{result: converter.DoclingConvertResult{Content: []byte("converted"), MediaType: "text/markdown"}}

	processor := worker.NewProcessor(repo, conv, storage, zap.NewNop(), worker.Config{MaxWorkers: 1, PollInterval: 10 * time.Millisecond})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	processor.Run(ctx)

	assertWithin(t, time.Second, func() bool {
		repo.markMutex.Lock()
		defer repo.markMutex.Unlock()
		return len(repo.markDoneIDs) == 1
	})

	if string(storage.uploaded) != "converted" {
		t.Fatalf("expected markdown to be uploaded")
	}
}

func assertWithin(t *testing.T, timeout time.Duration, condition func() bool) {
	t.Helper()

	deadline := time.After(timeout)
	for {
		select {
		case <-deadline:
			t.Fatalf("condition not met within %s", timeout)
		default:
			if condition() {
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
}
