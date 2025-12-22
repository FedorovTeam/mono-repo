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
	"github.com/mono-repo/table-extractor-service/internal/telemetry"
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
	LocalPath         string
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
	uc.logger.Debug("SubmitExtraction received",
		zap.String("document_id", req.DocumentID),
		zap.String("correlation_id", correlationID),
		zap.String("source_url", req.SourceURL),
		zap.String("local_path", req.LocalPath),
		zap.String("original_bucket", req.OriginalBucket),
		zap.String("original_object_key", req.OriginalObjectKey),
	)

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

	job, err := domain.NewTableExtractionJob(domain.NewTableExtractionJobParams{
		ID:              jobID,
		DocumentID:      req.DocumentID,
		SourceURL:       req.SourceURL,
		LocalPath:       req.LocalPath,
		Original:        domain.ArtifactLocation{Bucket: req.OriginalBucket, ObjectKey: req.OriginalObjectKey},
		ArtifactsBucket: artifactsBucket,
		ArtifactsPrefix: prefix,
		CorrelationID:   correlationID,
		CreatedAt:       now,
	})
	if err != nil {
		return domain.TableExtractionJob{}, fmt.Errorf("%w: %s", ErrInvalidArgument, err.Error())
	}

	job, _, err = uc.queue.UpsertOrGetActive(ctx, job, uc.cfg.QueueLimit)
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
	jobs, err := uc.queue.SelectPendingBatchForUpdate(ctx, uc.cfg.AcquireBatchSize, now)
	if err != nil {
		return false, fmt.Errorf("acquire pending jobs: %w", err)
	}
	if len(jobs) == 0 {
		uc.logger.Debug("no pending jobs found",
			zap.String("correlation_id", telemetry.CorrelationIDFromContext(ctx)),
			zap.Int("batch_size", uc.cfg.AcquireBatchSize),
		)
	}
	if len(jobs) == 0 {
		return false, nil
	}

	uc.logger.Debug("processing pending jobs",
		zap.Int("count", len(jobs)),
		zap.String("correlation_id", telemetry.CorrelationIDFromContext(ctx)),
	)

	for _, job := range jobs {
		startTime := uc.clock.Now().UTC()
		aggregate := domain.NewTableExtractionJobAggregate(job)
		started, _, err := aggregate.StartProcessing(startTime)
		if err != nil {
			return false, fmt.Errorf("start processing job %s: %w", job.ID, err)
		}
		startedJob := started.Job()

		if err := uc.queue.Save(ctx, startedJob); err != nil {
			return false, fmt.Errorf("save processing job %s: %w", job.ID, err)
		}

		uc.logger.Debug("started processing job",
			zap.String("queue_id", startedJob.ID.String()),
			zap.String("document_id", startedJob.DocumentID),
			zap.String("correlation_id", startedJob.CorrelationID),
		)

		if err := uc.processSingle(ctx, startedJob); err != nil {
			uc.logger.Error("failed to process extraction job",
				zap.Error(err),
				zap.String("queue_id", startedJob.ID.String()),
				zap.String("correlation_id", startedJob.CorrelationID),
			)
		}
	}

	return true, nil
}

func (uc *TableExtractorUseCase) processSingle(ctx context.Context, job domain.TableExtractionJob) error {
	ctxWithCID := telemetry.ContextWithCorrelationID(ctx, job.CorrelationID)
	ctxWithCID = telemetry.WithOutgoingCorrelationID(ctxWithCID, job.CorrelationID)
	aggregate := domain.NewTableExtractionJobAggregate(job)

	sourceProvided := strings.TrimSpace(job.SourceURL) != "" || strings.TrimSpace(job.LocalPath) != "" || (job.Original.Bucket != "" && job.Original.ObjectKey != "")
	uc.logger.Debug("processSingle started",
		zap.String("queue_id", job.ID.String()),
		zap.String("document_id", job.DocumentID),
		zap.String("correlation_id", job.CorrelationID),
		zap.Bool("has_source_url", strings.TrimSpace(job.SourceURL) != ""),
		zap.Bool("has_local_path", strings.TrimSpace(job.LocalPath) != ""),
		zap.Bool("has_original", job.Original.Bucket != "" && job.Original.ObjectKey != ""),
	)
	if !sourceProvided {
		now := uc.clock.Now().UTC()
		updated, _, transErr := aggregate.MarkFailed("original file is not set", "invalid_original", "source is empty", false, now)
		if transErr != nil {
			return errors.Join(errors.New("missing original location"), transErr)
		}
		saveErr := uc.queue.Save(ctxWithCID, updated.Job())
		uc.logger.Debug("job marked failed due to missing source",
			zap.String("queue_id", job.ID.String()),
			zap.String("document_id", job.DocumentID),
			zap.String("correlation_id", job.CorrelationID),
		)
		return errors.Join(errors.New("missing source"), saveErr)
	}

	sourceType := "original"
	if strings.TrimSpace(job.SourceURL) != "" {
		sourceType = "source_url"
	} else if strings.TrimSpace(job.LocalPath) != "" {
		sourceType = "local_path"
	}
	uc.logger.Debug("fetching source file",
		zap.String("queue_id", job.ID.String()),
		zap.String("document_id", job.DocumentID),
		zap.String("correlation_id", job.CorrelationID),
		zap.String("source_type", sourceType),
		zap.String("source_url", job.SourceURL),
		zap.String("local_path", job.LocalPath),
		zap.String("original_bucket", job.Original.Bucket),
		zap.String("original_object_key", job.Original.ObjectKey),
	)

	cached, err := uc.fileCache.Fetch(ctxWithCID, SourceLocation{
		Original:  job.Original,
		SourceURL: job.SourceURL,
		LocalPath: job.LocalPath,
	})
	if err != nil {
		now := uc.clock.Now().UTC()
		updated, _, transErr := aggregate.MarkFailed("download failed", "download_error", err.Error(), false, now)
		if transErr != nil {
			return errors.Join(fmt.Errorf("download original: %w", err), transErr)
		}
		statusErr := uc.queue.Save(ctxWithCID, updated.Job())
		uc.logger.Debug("job marked failed due to download error",
			zap.String("queue_id", job.ID.String()),
			zap.String("document_id", job.DocumentID),
			zap.String("correlation_id", job.CorrelationID),
			zap.Error(err),
		)
		return errors.Join(fmt.Errorf("download original: %w", err), statusErr)
	}
	uc.logger.Debug("source cached",
		zap.String("queue_id", job.ID.String()),
		zap.String("document_id", job.DocumentID),
		zap.String("correlation_id", job.CorrelationID),
		zap.String("cache_path", cached.Path),
	)
	defer func() {
		if cached.Cleanup != nil {
			cached.Cleanup()
			uc.logger.Debug("cache cleaned",
				zap.String("queue_id", job.ID.String()),
				zap.String("correlation_id", job.CorrelationID),
			)
		}
	}()

	uc.logger.Debug("starting extractor",
		zap.String("queue_id", job.ID.String()),
		zap.String("document_id", job.DocumentID),
		zap.String("correlation_id", job.CorrelationID),
		zap.String("source_path", cached.Path),
	)
	result, err := uc.extractor.Extract(ctxWithCID, cached.Path)
	if err != nil {
		now := uc.clock.Now().UTC()
		updated, _, transErr := aggregate.MarkFailed("extraction failed", "extraction_error", err.Error(), false, now)
		if transErr != nil {
			return errors.Join(fmt.Errorf("extract tables: %w", err), transErr)
		}
		statusErr := uc.queue.Save(ctxWithCID, updated.Job())
		uc.logger.Debug("job marked failed due to extraction error",
			zap.String("queue_id", job.ID.String()),
			zap.String("document_id", job.DocumentID),
			zap.String("correlation_id", job.CorrelationID),
			zap.Error(err),
		)
		return errors.Join(fmt.Errorf("extract tables: %w", err), statusErr)
	}
	uc.logger.Debug("extractor finished",
		zap.String("queue_id", job.ID.String()),
		zap.String("document_id", job.DocumentID),
		zap.String("correlation_id", job.CorrelationID),
		zap.Int("artifacts_count", len(result.DebugFiles)),
	)

	if err := uc.uploadArtifacts(ctxWithCID, job, result); err != nil {
		now := uc.clock.Now().UTC()
		updated, _, transErr := aggregate.MarkFailed("upload failed", "upload_error", err.Error(), true, now)
		if transErr != nil {
			return errors.Join(fmt.Errorf("upload artifacts: %w", err), transErr)
		}
		statusErr := uc.queue.Save(ctxWithCID, updated.Job())
		uc.logger.Debug("job marked failed due to upload error",
			zap.String("queue_id", job.ID.String()),
			zap.String("document_id", job.DocumentID),
			zap.String("correlation_id", job.CorrelationID),
			zap.Error(err),
		)
		return errors.Join(fmt.Errorf("upload artifacts: %w", err), statusErr)
	}

	finishedAt := uc.clock.Now().UTC()
	updated, _, transErr := aggregate.MarkDone("completed", finishedAt)
	if transErr != nil {
		return fmt.Errorf("mark job done: %w", transErr)
	}
	if err := uc.queue.Save(ctxWithCID, updated.Job()); err != nil {
		return fmt.Errorf("mark job done: %w", err)
	}

	uc.logger.Debug("processSingle finished",
		zap.String("queue_id", job.ID.String()),
		zap.String("document_id", job.DocumentID),
		zap.String("correlation_id", job.CorrelationID),
		zap.Time("finished_at", finishedAt),
	)

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
		uc.logger.Debug("uploading artifact",
			zap.String("queue_id", job.ID.String()),
			zap.String("document_id", job.DocumentID),
			zap.String("correlation_id", job.CorrelationID),
			zap.String("bucket", file.location.Bucket),
			zap.String("object_key", file.location.ObjectKey),
			zap.Int("size_bytes", len(file.payload)),
		)
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
		uc.logger.Debug("uploading debug artifact",
			zap.String("queue_id", job.ID.String()),
			zap.String("document_id", job.DocumentID),
			zap.String("correlation_id", job.CorrelationID),
			zap.String("bucket", job.Artifacts.DebugPrefix.Bucket),
			zap.String("object_key", objectKey),
			zap.Int("size_bytes", len(payload)),
		)
		if err := uc.storage.Upload(ctx, job.Artifacts.DebugPrefix.Bucket, objectKey, "text/plain", bytesReader(payload), int64(len(payload))); err != nil {
			return fmt.Errorf("upload debug file %s: %w", objectKey, err)
		}
	}

	return nil
}

func (uc *TableExtractorUseCase) validateSources(req SubmitExtractionRequest) error {
	hasSourceURL := strings.TrimSpace(req.SourceURL) != ""
	hasLocalPath := strings.TrimSpace(req.LocalPath) != ""
	hasBucketObject := strings.TrimSpace(req.OriginalBucket) != "" && strings.TrimSpace(req.OriginalObjectKey) != ""

	if !hasSourceURL && !hasLocalPath && !hasBucketObject {
		return fmt.Errorf("%w: provide source_url, local_path or original_bucket with original_object_key", ErrInvalidArgument)
	}

	if (strings.TrimSpace(req.OriginalBucket) == "") != (strings.TrimSpace(req.OriginalObjectKey) == "") {
		return fmt.Errorf("%w: both original_bucket and original_object_key must be set together", ErrInvalidArgument)
	}

	return nil
}

func (uc *TableExtractorUseCase) isPDF(req SubmitExtractionRequest) bool {
	ext := extractExtension(req.OriginalObjectKey)
	if ext == "" && req.SourceURL != "" {
		ext = extractExtension(req.SourceURL)
	}
	if ext == "" && req.LocalPath != "" {
		ext = extractExtension(req.LocalPath)
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
		aggregate := domain.NewTableExtractionJobAggregate(job)
		if maxAttempts > 0 && job.Attempts >= maxAttempts {
			updated, _, transErr := aggregate.MarkFailed("processing timeout exceeded", "processing_timeout", "processing exceeded timeout", false, now)
			if transErr != nil {
				return restored, failed, fmt.Errorf("mark job failed: %w", transErr)
			}
			if err := uc.queue.Save(ctx, updated.Job()); err != nil {
				return restored, failed, fmt.Errorf("mark job failed: %w", err)
			}
			uc.logger.Debug("job marked failed after timeout",
				zap.String("queue_id", job.ID.String()),
				zap.String("document_id", job.DocumentID),
				zap.String("correlation_id", job.CorrelationID),
				zap.Int("attempts", job.Attempts),
			)
			failed++
			continue
		}

		delay := backoffDelay(job.Attempts, backoffInterval, maxBackoff)
		nextRun := now.Add(delay)
		updated, _, transErr := aggregate.RestoreAfterTimeout(nextRun, "restored_after_timeout", now)
		if transErr != nil {
			return restored, failed, fmt.Errorf("postpone job: %w", transErr)
		}
		if err := uc.queue.Save(ctx, updated.Job()); err != nil {
			return restored, failed, fmt.Errorf("postpone job: %w", err)
		}
		uc.logger.Debug("job restored after timeout",
			zap.String("queue_id", job.ID.String()),
			zap.String("document_id", job.DocumentID),
			zap.String("correlation_id", job.CorrelationID),
			zap.Duration("backoff_delay", delay),
			zap.Time("next_run_at", nextRun),
			zap.Int("attempts", job.Attempts),
		)
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
	if count > 0 {
		uc.logger.Debug("jobs scheduled for retry",
			zap.Int("count", count),
			zap.Duration("retry_delay", delay),
			zap.Int("max_attempts", maxAttempts),
		)
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
	if count > 0 {
		uc.logger.Debug("cleanup finished jobs",
			zap.Int("removed", count),
			zap.Duration("retention", retention),
		)
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
