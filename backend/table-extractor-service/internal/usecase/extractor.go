package usecase

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/mono-repo/table-extractor-service/internal/domain"
	"github.com/mono-repo/table-extractor-service/internal/repository"
)

var (
	ErrInvalidArgument   = errors.New("invalid argument")
	ErrQueueLimitReached = errors.New("queue limit reached")
	ErrNotFound          = errors.New("job not found")
	ErrProcessorOffline  = errors.New("processor is not configured")
)

type SubmitExtractionRequest struct {
	DocumentID        string
	OriginalBucket    string
	OriginalObjectKey string
	SourceURL         string
	ArtifactsBucket   string
	ArtifactsPrefix   string
	WaitForResult     bool
	CorrelationID     string
}

// alias для совместимости с gRPC-слоем.
type ExtractTablesRequest = SubmitExtractionRequest

type Config struct {
	QueueLimit             int
	DefaultArtifactsBucket string
	DefaultArtifactsPrefix string
	AcquireBatchSize       int
}

type TableExtractorUseCase struct {
	queue     QueueRepo
	storage   StorageClient
	fileCache FileCache
	extractor Extractor
	cfg       Config
	clock     Clock
	idGen     IDGenerator
	logger    *zap.Logger
}

type Dependencies struct {
	Queue       QueueRepo
	Storage     StorageClient
	FileCache   FileCache
	Extractor   Extractor
	Clock       Clock
	IDGenerator IDGenerator
}

func NewTableExtractorUseCase(deps Dependencies, cfg Config, logger *zap.Logger) *TableExtractorUseCase {
	if logger == nil {
		logger = zap.NewNop()
	}
	if cfg.AcquireBatchSize <= 0 {
		cfg.AcquireBatchSize = 1
	}
	return &TableExtractorUseCase{
		queue:     deps.Queue,
		storage:   deps.Storage,
		fileCache: deps.FileCache,
		extractor: deps.Extractor,
		cfg:       cfg,
		clock:     resolveClock(deps.Clock),
		idGen:     resolveIDGenerator(deps.IDGenerator),
		logger:    logger,
	}
}

func (uc *TableExtractorUseCase) ExtractTablesFromPDF(ctx context.Context, req ExtractTablesRequest) (domain.TableExtractionJob, error) {
	return uc.SubmitExtraction(ctx, SubmitExtractionRequest(req))
}

func (uc *TableExtractorUseCase) GetJob(ctx context.Context, id uuid.UUID) (domain.TableExtractionJob, error) {
	job, ok, err := uc.queue.GetByID(ctx, id)
	if err != nil {
		return domain.TableExtractionJob{}, err
	}
	if !ok {
		return domain.TableExtractionJob{}, ErrNotFound
	}
	return job, nil
}

func (uc *TableExtractorUseCase) SubmitExtraction(ctx context.Context, req SubmitExtractionRequest) (domain.TableExtractionJob, error) {
	if strings.TrimSpace(req.DocumentID) == "" {
		return domain.TableExtractionJob{}, fmt.Errorf("%w: document_id is required", ErrInvalidArgument)
	}
	if err := uc.validateSources(req); err != nil {
		return domain.TableExtractionJob{}, err
	}
	if !uc.isPDF(req) {
		return domain.TableExtractionJob{}, fmt.Errorf("%w: only PDF documents are supported", ErrInvalidArgument)
	}

	correlationID := strings.TrimSpace(req.CorrelationID)
	if correlationID == "" {
		return domain.TableExtractionJob{}, fmt.Errorf("%w: correlation_id is required", ErrInvalidArgument)
	}

	now := uc.clock.Now().UTC()
	artifactsBucket := req.ArtifactsBucket
	if artifactsBucket == "" {
		artifactsBucket = uc.cfg.DefaultArtifactsBucket
	}
	if artifactsBucket == "" {
		return domain.TableExtractionJob{}, fmt.Errorf("%w: artifacts_bucket is required", ErrInvalidArgument)
	}

	jobID := uc.idGen.New()
	prefix := req.ArtifactsPrefix
	if prefix == "" {
		prefix = path.Join(uc.cfg.DefaultArtifactsPrefix, req.DocumentID, jobID.String())
	}

	job := domain.TableExtractionJob{
		ID:              jobID,
		DocumentID:      req.DocumentID,
		Original:        domain.ArtifactLocation{Bucket: req.OriginalBucket, ObjectKey: req.OriginalObjectKey},
		ArtifactsBucket: artifactsBucket,
		ArtifactsPrefix: prefix,
		Artifacts:       domain.BuildArtifacts(artifactsBucket, prefix),
		Status:          domain.StatusPending,
		Attempts:        0,
		CorrelationID:   correlationID,
		CreatedAt:       now,
		UpdatedAt:       now,
	}

	job, _, err := uc.queue.UpsertOrGetActive(ctx, job, uc.cfg.QueueLimit)
	if err != nil {
		if errors.Is(err, repository.ErrQueueLimitReached) {
			return domain.TableExtractionJob{}, ErrQueueLimitReached
		}
		return domain.TableExtractionJob{}, err
	}

	return job, nil
}

// ProcessExtractionJob — бизнес-логика воркера: взять pending-задачу, извлечь таблицы и записать артефакты.
func (uc *TableExtractorUseCase) ProcessExtractionJob(ctx context.Context) (bool, error) {
	if uc.extractor == nil || uc.storage == nil || uc.fileCache == nil {
		return false, ErrProcessorOffline
	}

	now := uc.clock.Now().UTC()
	jobs, err := uc.queue.AcquirePendingBatch(ctx, uc.cfg.AcquireBatchSize, now)
	if err != nil {
		return false, fmt.Errorf("acquire pending jobs: %w", err)
	}
	if len(jobs) == 0 {
		return false, nil
	}

	for _, job := range jobs {
		if err := uc.processSingle(ctx, job); err != nil {
			uc.logger.Error("failed to process extraction job",
				zap.Error(err),
				zap.String("queue_id", job.ID.String()),
				zap.String("correlation_id", job.CorrelationID),
			)
		}
	}

	return true, nil
}

func (uc *TableExtractorUseCase) processSingle(ctx context.Context, job domain.TableExtractionJob) error {
	if job.Original.Bucket == "" || job.Original.ObjectKey == "" {
		statusErr := uc.queue.UpdateStatus(ctx, job.ID, domain.StatusFailed, "original file is not set", "invalid_original", "original location is empty", ptrTime(uc.clock.Now().UTC()))
		return errors.Join(errors.New("missing original location"), statusErr)
	}

	cached, err := uc.fileCache.Fetch(ctx, job.Original)
	if err != nil {
		statusErr := uc.queue.UpdateStatus(ctx, job.ID, domain.StatusFailed, "download failed", "download_error", err.Error(), ptrTime(uc.clock.Now().UTC()))
		return errors.Join(fmt.Errorf("download original: %w", err), statusErr)
	}
	defer func() {
		if cached.Cleanup != nil {
			cached.Cleanup()
		}
	}()

	result, err := uc.extractor.Extract(ctx, cached.Path)
	if err != nil {
		statusErr := uc.queue.UpdateStatus(ctx, job.ID, domain.StatusFailed, "extraction failed", "extraction_error", err.Error(), ptrTime(uc.clock.Now().UTC()))
		return errors.Join(fmt.Errorf("extract tables: %w", err), statusErr)
	}

	if err := uc.uploadArtifacts(ctx, job, result); err != nil {
		statusErr := uc.queue.UpdateStatus(ctx, job.ID, domain.StatusFailedRetryable, "upload failed", "upload_error", err.Error(), ptrTime(uc.clock.Now().UTC()))
		return errors.Join(fmt.Errorf("upload artifacts: %w", err), statusErr)
	}

	finishedAt := uc.clock.Now().UTC()
	if err := uc.queue.UpdateStatus(ctx, job.ID, domain.StatusDone, "completed", "", "", &finishedAt); err != nil {
		return fmt.Errorf("mark job done: %w", err)
	}

	return nil
}

func (uc *TableExtractorUseCase) uploadArtifacts(ctx context.Context, job domain.TableExtractionJob, result ExtractionResult) error {
	uploads := []struct {
		location    domain.ArtifactLocation
		payload     []byte
		contentType string
	}{
		{job.Artifacts.RawJSON, result.RawJSON, "application/json"},
		{job.Artifacts.NormalizedJSON, result.NormalizedJSON, "application/json"},
		{job.Artifacts.Markdown, result.Markdown, "text/markdown"},
		{job.Artifacts.Manifest, result.Manifest, "application/json"},
	}

	for _, file := range uploads {
		if len(file.payload) == 0 {
			continue
		}
		if err := uc.storage.Upload(ctx, file.location.Bucket, file.location.ObjectKey, file.contentType, bytesReader(file.payload), int64(len(file.payload))); err != nil {
			return fmt.Errorf("upload %s: %w", file.location.ObjectKey, err)
		}
	}

	if len(result.DebugFiles) == 0 {
		return nil
	}

	for name, payload := range result.DebugFiles {
		if len(payload) == 0 {
			continue
		}
		objectKey := path.Join(job.Artifacts.DebugPrefix.ObjectKey, filepath.Base(name))
		if err := uc.storage.Upload(ctx, job.Artifacts.DebugPrefix.Bucket, objectKey, "text/plain", bytesReader(payload), int64(len(payload))); err != nil {
			return fmt.Errorf("upload debug file %s: %w", objectKey, err)
		}
	}

	return nil
}

func (uc *TableExtractorUseCase) validateSources(req SubmitExtractionRequest) error {
	if req.SourceURL == "" {
		if req.OriginalBucket == "" || req.OriginalObjectKey == "" {
			return fmt.Errorf("%w: original_bucket and original_object_key are required", ErrInvalidArgument)
		}
		return nil
	}

	if req.OriginalBucket != "" || req.OriginalObjectKey != "" {
		return fmt.Errorf("%w: source_url cannot be combined with bucket/object", ErrInvalidArgument)
	}
	return nil
}

func (uc *TableExtractorUseCase) isPDF(req SubmitExtractionRequest) bool {
	ext := extractExtension(req.OriginalObjectKey)
	if ext == "" && req.SourceURL != "" {
		ext = extractExtension(req.SourceURL)
	}
	return ext == "pdf"
}

func bytesReader(data []byte) io.Reader {
	return bytes.NewReader(data)
}

func ptrTime(t time.Time) *time.Time {
	return &t
}

func extractExtension(value string) string {
	clean := value
	if idx := strings.IndexAny(clean, "?#"); idx >= 0 {
		clean = clean[:idx]
	}
	return strings.ToLower(strings.TrimPrefix(path.Ext(clean), "."))
}

func (uc *TableExtractorUseCase) RestoreStuckProcessing(ctx context.Context, processingTimeout time.Duration, maxAttempts int, backoffInterval, maxBackoff time.Duration) (int, int, error) {
	now := uc.clock.Now().UTC()
	threshold := now
	if processingTimeout > 0 {
		threshold = now.Add(-processingTimeout)
	}

	jobs, err := uc.queue.ListProcessingBefore(ctx, threshold)
	if err != nil {
		return 0, 0, fmt.Errorf("list stuck processing: %w", err)
	}

	restored := 0
	failed := 0

	for _, job := range jobs {
		if maxAttempts > 0 && job.Attempts >= maxAttempts {
			if err := uc.queue.UpdateStatus(ctx, job.ID, domain.StatusFailed, "processing timeout exceeded", "processing_timeout", "processing exceeded timeout", &now); err != nil {
				return restored, failed, fmt.Errorf("mark job failed: %w", err)
			}
			failed++
			continue
		}

		delay := backoffDelay(job.Attempts, backoffInterval, maxBackoff)
		nextRun := now.Add(delay)
		if err := uc.queue.Postpone(ctx, job.ID, nextRun, "restored_after_timeout"); err != nil {
			return restored, failed, fmt.Errorf("postpone job: %w", err)
		}
		restored++
	}

	return restored, failed, nil
}

func (uc *TableExtractorUseCase) RetryFailed(ctx context.Context, maxAttempts int, retryDelay time.Duration) (int, error) {
	delay := retryDelay
	if delay < 0 {
		delay = 0
	}
	now := uc.clock.Now().UTC()
	count, err := uc.queue.RetryFailed(ctx, maxAttempts, delay, now)
	if err != nil {
		return 0, fmt.Errorf("retry failed jobs: %w", err)
	}
	return count, nil
}

func (uc *TableExtractorUseCase) CleanupFinished(ctx context.Context, retention time.Duration) (int, error) {
	if retention <= 0 {
		return 0, nil
	}
	cutoff := uc.clock.Now().UTC().Add(-retention)
	count, err := uc.queue.Cleanup(ctx, cutoff)
	if err != nil {
		return 0, fmt.Errorf("cleanup finished jobs: %w", err)
	}
	return count, nil
}

func backoffDelay(attempts int, base, max time.Duration) time.Duration {
	if base <= 0 {
		return 0
	}
	delay := base * time.Duration(attempts+1)
	if max > 0 && delay > max {
		return max
	}
	return delay
}
