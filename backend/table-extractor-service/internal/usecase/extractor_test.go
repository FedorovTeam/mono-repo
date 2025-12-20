package usecase

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/mono-repo/table-extractor-service/internal/domain"
	"github.com/mono-repo/table-extractor-service/internal/repository/memory"
	"github.com/mono-repo/table-extractor-service/internal/telemetry"
)

func TestSubmitExtractionIdempotencyByDocument(t *testing.T) {
	repo := memory.NewInMemoryRepository()
	uc := NewTableExtractorUseCase(Dependencies{
		Queue: repo,
	}, Config{
		QueueLimit:             10,
		DefaultArtifactsBucket: "artifacts",
		DefaultArtifactsPrefix: "tables",
	}, nil)

	ctx, correlationID := telemetry.EnsureCorrelationID(context.Background())
	job, err := uc.ExtractTablesFromPDF(ctx, ExtractTablesRequest{
		DocumentID:        "doc-1",
		OriginalBucket:    "bucket",
		OriginalObjectKey: "path/to.pdf",
		CorrelationID:     correlationID,
	})
	require.NoError(t, err)
	require.Equal(t, correlationID, job.CorrelationID)
	require.Equal(t, "doc-1", job.DocumentID)
	require.Equal(t, "artifacts", job.Artifacts.Markdown.Bucket)
	require.Equal(t, "tables/doc-1/"+job.ID.String()+"/tables.md", job.Artifacts.Markdown.ObjectKey)

	secondJob, err := uc.ExtractTablesFromPDF(context.Background(), ExtractTablesRequest{
		DocumentID:        "doc-1",
		OriginalBucket:    "bucket",
		OriginalObjectKey: "path/to.pdf",
		CorrelationID:     "another-correlation",
	})
	require.NoError(t, err)
	require.Equal(t, job.ID, secondJob.ID, "idempotent by document_id")
	require.Equal(t, job.CorrelationID, secondJob.CorrelationID, "keeps original correlation id")
}

func TestSubmitExtractionValidatesPdfExtension(t *testing.T) {
	repo := memory.NewInMemoryRepository()
	uc := NewTableExtractorUseCase(Dependencies{
		Queue: repo,
	}, Config{
		QueueLimit:             1,
		DefaultArtifactsBucket: "artifacts",
		DefaultArtifactsPrefix: "tables",
	}, nil)

	_, err := uc.ExtractTablesFromPDF(context.Background(), ExtractTablesRequest{
		DocumentID:        "doc-1",
		OriginalBucket:    "bucket",
		OriginalObjectKey: "path/to.docx",
	})
	require.ErrorIs(t, err, ErrInvalidArgument)
}

func TestSubmitExtractionQueueLimit(t *testing.T) {
	repo := memory.NewInMemoryRepository()
	uc := NewTableExtractorUseCase(Dependencies{
		Queue: repo,
	}, Config{
		QueueLimit:             1,
		DefaultArtifactsBucket: "artifacts",
		DefaultArtifactsPrefix: "tables",
	}, nil)

	ctx, _ := telemetry.EnsureCorrelationID(context.Background())
	_, err := uc.ExtractTablesFromPDF(ctx, ExtractTablesRequest{
		DocumentID:      "doc-1",
		SourceURL:       "https://example.com/source.pdf",
		ArtifactsBucket: "artifacts",
		ArtifactsPrefix: "tables/doc-1",
		WaitForResult:   true,
		CorrelationID:   "",
	})
	require.NoError(t, err)

	_, err = uc.ExtractTablesFromPDF(context.Background(), ExtractTablesRequest{
		DocumentID:      "doc-2",
		SourceURL:       "https://example.com/source2.pdf",
		ArtifactsBucket: "artifacts",
	})
	require.ErrorIs(t, err, ErrQueueLimitReached)
}

func TestProcessExtractionJobUploadsArtifacts(t *testing.T) {
	repo := memory.NewInMemoryRepository()
	storage := newFakeStorage()
	originalKey := "source/doc.pdf"
	storage.addObject("originals", originalKey, []byte("%PDF-1.4"))

	jobID := uuid.MustParse("00000000-0000-0000-0000-000000000001")
	now := time.Date(2024, 5, 10, 9, 0, 0, 0, time.UTC)
	extractor := &fakeExtractor{
		result: ExtractionResult{
			RawJSON:        []byte(`{"raw":true}`),
			NormalizedJSON: []byte(`{"normalized":true}`),
			Markdown:       []byte("# tables"),
			Manifest:       []byte(`{"version":1}`),
			DebugFiles: map[string][]byte{
				"page-1.txt": []byte("debug"),
			},
		},
	}

	uc := NewTableExtractorUseCase(Dependencies{
		Queue:       repo,
		Storage:     storage,
		Extractor:   extractor,
		Clock:       fixedClock{now: now},
		IDGenerator: fixedIDGenerator{id: jobID},
	}, Config{
		QueueLimit:             10,
		DefaultArtifactsBucket: "artifacts",
		DefaultArtifactsPrefix: "tables",
	}, nil)

	ctx := context.Background()
	job, err := uc.SubmitExtraction(ctx, SubmitExtractionRequest{
		DocumentID:        "doc-1",
		OriginalBucket:    "originals",
		OriginalObjectKey: originalKey,
	})
	require.NoError(t, err)

	processed, err := uc.ProcessExtractionJob(ctx)
	require.NoError(t, err)
	require.True(t, processed)

	storedJob, err := uc.GetJob(ctx, job.ID)
	require.NoError(t, err)
	require.Equal(t, domain.StatusDone, storedJob.Status)
	require.NotNil(t, storedJob.FinishedAt)
	require.True(t, storedJob.FinishedAt.After(now.Add(-time.Millisecond)))

	storage.assertObject(t, job.Artifacts.RawJSON, []byte(`{"raw":true}`))
	storage.assertObject(t, job.Artifacts.NormalizedJSON, []byte(`{"normalized":true}`))
	storage.assertObject(t, job.Artifacts.Markdown, []byte("# tables"))
	storage.assertObject(t, job.Artifacts.Manifest, []byte(`{"version":1}`))
	storage.assertObject(t, domain.ArtifactLocation{
		Bucket:    job.Artifacts.DebugPrefix.Bucket,
		ObjectKey: job.Artifacts.DebugPrefix.ObjectKey + "/page-1.txt",
	}, []byte("debug"))
}

func TestGetJobNotFound(t *testing.T) {
	repo := memory.NewInMemoryRepository()
	uc := NewTableExtractorUseCase(Dependencies{
		Queue: repo,
	}, Config{
		QueueLimit:             0,
		DefaultArtifactsBucket: "artifacts",
		DefaultArtifactsPrefix: "tables",
	}, nil)

	_, err := uc.GetJob(context.Background(), uuid.MustParse("00000000-0000-0000-0000-000000000000"))
	require.ErrorIs(t, err, ErrNotFound)
}

func TestRestoreStuckProcessingReschedules(t *testing.T) {
	repo := memory.NewInMemoryRepository()
	now := time.Date(2024, 6, 1, 12, 0, 0, 0, time.UTC)
	uc := NewTableExtractorUseCase(Dependencies{
		Queue: repo,
		Clock: fixedClock{now: now},
	}, Config{
		QueueLimit:             10,
		DefaultArtifactsBucket: "artifacts",
		DefaultArtifactsPrefix: "tables",
	}, nil)

	stuckJob := domain.TableExtractionJob{
		ID:              uuid.MustParse("00000000-0000-0000-0000-000000000010"),
		DocumentID:      "doc-stuck",
		Status:          domain.StatusProcessing,
		Attempts:        1,
		ArtifactsBucket: "artifacts",
		ArtifactsPrefix: "tables/doc-stuck/stuck",
		Original:        domain.ArtifactLocation{Bucket: "orig", ObjectKey: "doc-stuck.pdf"},
		CreatedAt:       now.Add(-2 * time.Hour),
		UpdatedAt:       now.Add(-time.Hour),
	}
	_, _, err := repo.UpsertOrGetActive(context.Background(), stuckJob, 10)
	require.NoError(t, err)

	restored, failed, err := uc.RestoreStuckProcessing(context.Background(), 30*time.Minute, 3, 5*time.Second, time.Minute)
	require.NoError(t, err)
	require.Equal(t, 1, restored)
	require.Zero(t, failed)

	updated, err := uc.GetJob(context.Background(), stuckJob.ID)
	require.NoError(t, err)
	require.Equal(t, domain.StatusPending, updated.Status)
	require.NotNil(t, updated.NextRunAt)
	require.WithinDuration(t, now.Add(10*time.Second), *updated.NextRunAt, time.Millisecond)
}

func TestRestoreStuckProcessingMarksFailed(t *testing.T) {
	repo := memory.NewInMemoryRepository()
	now := time.Date(2024, 6, 1, 12, 0, 0, 0, time.UTC)
	uc := NewTableExtractorUseCase(Dependencies{
		Queue: repo,
		Clock: fixedClock{now: now},
	}, Config{
		QueueLimit:             10,
		DefaultArtifactsBucket: "artifacts",
		DefaultArtifactsPrefix: "tables",
	}, nil)

	stuckJob := domain.TableExtractionJob{
		ID:              uuid.MustParse("00000000-0000-0000-0000-000000000011"),
		DocumentID:      "doc-failed",
		Status:          domain.StatusProcessing,
		Attempts:        5,
		ArtifactsBucket: "artifacts",
		ArtifactsPrefix: "tables/doc-failed/stuck",
		Original:        domain.ArtifactLocation{Bucket: "orig", ObjectKey: "doc-failed.pdf"},
		CreatedAt:       now.Add(-2 * time.Hour),
		UpdatedAt:       now.Add(-time.Hour),
	}
	_, _, err := repo.UpsertOrGetActive(context.Background(), stuckJob, 10)
	require.NoError(t, err)

	restored, failed, err := uc.RestoreStuckProcessing(context.Background(), 20*time.Minute, 3, 5*time.Second, 20*time.Second)
	require.NoError(t, err)
	require.Zero(t, restored)
	require.Equal(t, 1, failed)

	updated, err := uc.GetJob(context.Background(), stuckJob.ID)
	require.NoError(t, err)
	require.Equal(t, domain.StatusFailed, updated.Status)
	require.NotNil(t, updated.FinishedAt)
}

func TestRetryFailedAndCleanupFinished(t *testing.T) {
	repo := memory.NewInMemoryRepository()
	now := time.Date(2024, 6, 1, 12, 0, 0, 0, time.UTC)
	uc := NewTableExtractorUseCase(Dependencies{
		Queue: repo,
		Clock: fixedClock{now: now},
	}, Config{
		QueueLimit:             10,
		DefaultArtifactsBucket: "artifacts",
		DefaultArtifactsPrefix: "tables",
	}, nil)

	failedJob := domain.TableExtractionJob{
		ID:              uuid.MustParse("00000000-0000-0000-0000-000000000012"),
		DocumentID:      "doc-failed",
		Status:          domain.StatusFailed,
		Attempts:        1,
		ArtifactsBucket: "artifacts",
		ArtifactsPrefix: "tables/doc-failed/stuck",
		Original:        domain.ArtifactLocation{Bucket: "orig", ObjectKey: "doc-failed.pdf"},
		CreatedAt:       now.Add(-2 * time.Hour),
		UpdatedAt:       now.Add(-2 * time.Hour),
		FinishedAt:      ptrTime(now.Add(-time.Hour)),
	}
	_, _, err := repo.UpsertOrGetActive(context.Background(), failedJob, 10)
	require.NoError(t, err)

	doneJob := domain.TableExtractionJob{
		ID:              uuid.MustParse("00000000-0000-0000-0000-000000000013"),
		DocumentID:      "doc-done",
		Status:          domain.StatusDone,
		Attempts:        1,
		ArtifactsBucket: "artifacts",
		ArtifactsPrefix: "tables/doc-done/stuck",
		Original:        domain.ArtifactLocation{Bucket: "orig", ObjectKey: "doc-done.pdf"},
		CreatedAt:       now.Add(-3 * time.Hour),
		UpdatedAt:       now.Add(-3 * time.Hour),
		FinishedAt:      ptrTime(now.Add(-2 * time.Hour)),
	}
	_, _, err = repo.UpsertOrGetActive(context.Background(), doneJob, 10)
	require.NoError(t, err)

	retried, err := uc.RetryFailed(context.Background(), 3, 15*time.Second)
	require.NoError(t, err)
	require.Equal(t, 1, retried)

	updated, err := uc.GetJob(context.Background(), failedJob.ID)
	require.NoError(t, err)
	require.Equal(t, domain.StatusPending, updated.Status)
	require.NotNil(t, updated.NextRunAt)
	require.WithinDuration(t, now.Add(15*time.Second), *updated.NextRunAt, time.Millisecond)

	cleaned, err := uc.CleanupFinished(context.Background(), time.Hour)
	require.NoError(t, err)
	require.Equal(t, 1, cleaned)

	_, err = uc.GetJob(context.Background(), doneJob.ID)
	require.ErrorIs(t, err, ErrNotFound)
}

type fakeExtractor struct {
	result ExtractionResult
	err    error
}

func (f *fakeExtractor) Extract(_ context.Context, _ string) (ExtractionResult, error) {
	if f.err != nil {
		return ExtractionResult{}, f.err
	}
	return f.result, nil
}

type fixedClock struct {
	now time.Time
}

func (f fixedClock) Now() time.Time {
	return f.now
}

type fixedIDGenerator struct {
	id uuid.UUID
}

func (f fixedIDGenerator) New() uuid.UUID {
	return f.id
}

type fakeStorage struct {
	mu      sync.Mutex
	objects map[string][]byte
}

func newFakeStorage() *fakeStorage {
	return &fakeStorage{
		objects: make(map[string][]byte),
	}
}

func (s *fakeStorage) addObject(bucket, key string, data []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.objects[s.composeKey(bucket, key)] = append([]byte(nil), data...)
}

func (s *fakeStorage) Download(_ context.Context, bucket, objectKey string) (io.ReadCloser, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, ok := s.objects[s.composeKey(bucket, objectKey)]
	if !ok {
		return nil, fmt.Errorf("not found")
	}
	return io.NopCloser(bytes.NewReader(data)), nil
}

func (s *fakeStorage) Upload(_ context.Context, bucket, objectKey, _ string, content io.Reader, _ int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, err := io.ReadAll(content)
	if err != nil {
		return err
	}
	s.objects[s.composeKey(bucket, objectKey)] = data
	return nil
}

func (s *fakeStorage) composeKey(bucket, key string) string {
	return bucket + "/" + key
}

func (s *fakeStorage) assertObject(t *testing.T, location domain.ArtifactLocation, expected []byte) {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	actual, ok := s.objects[s.composeKey(location.Bucket, location.ObjectKey)]
	require.True(t, ok, "object %s/%s not found", location.Bucket, location.ObjectKey)
	require.Equal(t, expected, actual)
}
