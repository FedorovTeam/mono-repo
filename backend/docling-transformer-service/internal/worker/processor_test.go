package worker

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/mono-repo/docling-transformer-service/internal/converter"
	"github.com/mono-repo/docling-transformer-service/internal/repository"
)

type pollingRepoStub struct {
	updateInfoCalls  int
	markDoneCalls    int
	markWaitingCalls int
}

func (p *pollingRepoStub) Enqueue(context.Context, repository.EnqueueParams) (uuid.UUID, error) {
	return uuid.Nil, nil
}
func (p *pollingRepoStub) CountActive(context.Context) (int, error) { return 0, nil }
func (p *pollingRepoStub) AcquireNext(context.Context) (*repository.TransformationTask, error) {
	return nil, nil
}
func (p *pollingRepoStub) MarkDone(context.Context, uuid.UUID, string, string) error {
	p.markDoneCalls++
	return nil
}
func (p *pollingRepoStub) MarkFailed(context.Context, uuid.UUID, string) error { return nil }
func (p *pollingRepoStub) UpdateDoclingInfo(context.Context, uuid.UUID, string, int) error {
	p.updateInfoCalls++
	return nil
}
func (p *pollingRepoStub) MarkDoclingWaiting(context.Context, uuid.UUID, string, int, time.Time) error {
	p.markWaitingCalls++
	return nil
}
func (p *pollingRepoStub) ActivateWaiting(context.Context, time.Time, time.Duration) (int, int, error) {
	return 0, 0, nil
}
func (p *pollingRepoStub) RestoreStuckProcessing(context.Context, time.Duration, int, int) (int, int, error) {
	return 0, 0, nil
}
func (p *pollingRepoStub) CleanupFinished(context.Context, time.Duration) (int, error) { return 0, nil }
func (p *pollingRepoStub) CountByStatus(context.Context) (int, int, int, error)        { return 0, 0, 0, nil }

type statusOnlyConverter struct {
	submitCalled bool
}

func (c *statusOnlyConverter) Submit(context.Context, converter.DoclingConvertRequest) (string, error) {
	c.submitCalled = true
	return "", nil
}

func (c *statusOnlyConverter) CheckStatus(context.Context, string) (converter.StatusCheckResult, error) {
	return converter.StatusCheckResult{Status: "success", Result: &converter.DoclingConvertResult{Content: []byte("markdown"), MediaType: "text/markdown"}}, nil
}

type trackingStorage struct {
	downloadCalls int
	uploaded      []byte
}

func (s *trackingStorage) Download(context.Context, string, string) ([]byte, error) {
	s.downloadCalls++
	return []byte("original"), nil
}

func (s *trackingStorage) Upload(_ context.Context, _, _ string, _ string, content []byte) error {
	s.uploaded = append([]byte(nil), content...)
	return nil
}

func TestHandleTaskPollsWithoutRedownloading(t *testing.T) {
	repo := &pollingRepoStub{}
	storage := &trackingStorage{}
	conv := &statusOnlyConverter{}
	processor := NewProcessor(repo, conv, storage, nil, Config{})

	task := &repository.TransformationTask{
		ID:                uuid.New(),
		DoclingJobID:      "job-123",
		DoclingPollCount:  2,
		MarkdownBucket:    "md",
		MarkdownObjectKey: "dest/file.md",
		CreatedAt:         time.Now().Add(-time.Minute),
	}

	if err := processor.handleTask(context.Background(), task); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if storage.downloadCalls != 0 {
		t.Fatalf("expected no downloads during polling, got %d", storage.downloadCalls)
	}

	if repo.markDoneCalls != 1 {
		t.Fatalf("expected mark done to be called once, got %d", repo.markDoneCalls)
	}

	if repo.updateInfoCalls != 1 {
		t.Fatalf("expected docling info update once, got %d", repo.updateInfoCalls)
	}

	if conv.submitCalled {
		t.Fatal("submit should not be called when job id is already set")
	}
}
