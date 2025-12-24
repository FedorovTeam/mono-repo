package document

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log"
	"path"
	"strings"
	"text/template"
	"time"

	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/types/known/durationpb"

	"backend-document-service/internal/domain"
	workersvc "backend-document-service/internal/usecase"
	githubpublisherv1 "backend-document-service/proto/backend/githubpublisher/v1"
	preprocessv1 "backend-document-service/proto/backend/preprocess/v1"
	tableextractorv1 "backend-document-service/proto/backend/tableextractor/v1"
)

// StageWorker обрабатывает pending-задачи из processing_jobs.
type StageWorker struct {
	repo        Repository
	activities  *Activities
	stage       domain.JobStage
	batchSize   int
	maxBatches  int
	metrics     *workerMetrics
	service     *workersvc.WorkerService
	retryDelay  time.Duration
	maxAttempts int
	router      *domain.StageRouter
}

func NewStageWorker(repo Repository, activities *Activities, stage domain.JobStage, batchSize int, retryDelay time.Duration, router *domain.StageRouter) *StageWorker {
	if batchSize <= 0 {
		batchSize = 100
	}
	if retryDelay <= 0 {
		retryDelay = time.Minute
	}

	maxBatches := 0
	if stage == domain.JobStageConvertToMarkdown {
		batchSize = 1
		maxBatches = 1
	}
	const defaultMaxAttempts = 5

	service := workersvc.NewWorkerService(repo, repo, retryDelay)
	service.SetLogger(log.Printf)

	return &StageWorker{
		repo:        repo,
		activities:  activities,
		stage:       stage,
		batchSize:   batchSize,
		maxBatches:  maxBatches,
		metrics:     newWorkerMetrics(fmt.Sprintf("stage_%s", stage)),
		service:     service,
		retryDelay:  retryDelay,
		maxAttempts: defaultMaxAttempts,
		router:      router,
	}
}

func (w *StageWorker) Run(ctx context.Context, interval time.Duration) error {
	handler := func(ctx context.Context, job domain.ProcessingJob) (workersvc.JobResult, error) {
		start := time.Now()
		result, err := w.handleJob(ctx, job)
		w.metrics.recordDuration(ctx, time.Since(start))
		if err != nil {
			w.metrics.recordError(ctx)
		}
		return result, err
	}

	if w.maxBatches == 1 {
		return w.service.RunWithBatchLimit(ctx, w.stage, interval, w.batchSize, w.maxBatches, handler, w.metrics.observeBatch)
	}

	return w.service.Run(ctx, w.stage, interval, w.batchSize, handler, w.metrics.observeBatch)
}

func (w *StageWorker) handleJob(ctx context.Context, job domain.ProcessingJob) (workersvc.JobResult, error) {
	switch job.Stage {
	case domain.JobStageStoreOriginal:
		return w.handleStoreOriginal(ctx, job)
	case domain.JobStagePreprocessToDocx:
		return w.handlePreprocessToDocx(ctx, job)
	case domain.JobStageExtractTables:
		return w.handleExtractTables(ctx, job)
	case domain.JobStageConvertToMarkdown:
		return w.handleConvert(ctx, job)
	case domain.JobStagePublishToGitHub:
		return w.handlePublish(ctx, job)
	default:
		return workersvc.JobResult{Payload: job.Payload}, fmt.Errorf("unsupported stage %s", job.Stage)
	}
	return workersvc.JobResult{Payload: job.Payload}, nil
}

func (w *StageWorker) handleStoreOriginal(ctx context.Context, job domain.ProcessingJob) (workersvc.JobResult, error) {
	payload := domain.StoreOriginalPayloadFromMap(job.Payload)
	correlationID := payload.CorrelationID
	if correlationID == "" {
		correlationID = correlationIDFromContext(ctx)
	}
	if w.activities == nil {
		return workersvc.JobResult{Payload: payload.ToMap()}, errors.New("activities not configured")
	}

	if err := w.activities.MarkStoredOriginal(w.jobContext(ctx, correlationID), job.DocumentID, payload.OriginalETag); err != nil {
		return workersvc.JobResult{Payload: payload.ToMap()}, err
	}

	return workersvc.JobResult{Payload: payload.ToMap(), Event: &domain.JobEvent{EventType: "completed"}}, nil
}

func (w *StageWorker) handlePreprocessToDocx(ctx context.Context, job domain.ProcessingJob) (workersvc.JobResult, error) {
	payload := domain.PreprocessPayloadFromMap(job.Payload)
	if w.activities == nil {
		return workersvc.JobResult{Payload: payload.ToMap()}, errors.New("activities not configured")
	}

	doc, err := w.repo.GetDocument(ctx, job.DocumentID)
	if err != nil {
		return workersvc.JobResult{Payload: payload.ToMap()}, fmt.Errorf("load document: %w", err)
	}
	if doc == nil {
		return workersvc.JobResult{Payload: payload.ToMap()}, fmt.Errorf("document %s not found", job.DocumentID)
	}

	payload.OriginalBucket = firstNonEmpty(payload.OriginalBucket, doc.OriginalBucket)
	payload.OriginalObjectKey = firstNonEmpty(payload.OriginalObjectKey, doc.OriginalObjectKey)
	payload.OriginalETag = firstNonEmpty(payload.OriginalETag, latestVersion(doc).OriginalETag)
	payload.TargetBucket = firstNonEmpty(payload.TargetBucket, doc.OriginalBucket)
	if payload.TargetObjectKey == "" {
		payload.TargetObjectKey = preprocessTargetKey(payload.OriginalObjectKey)
	}
	payload.FilenameExtension = firstNonEmpty(
		payload.FilenameExtension,
		fileExtension(payload.OriginalObjectKey),
		fileExtension(doc.OriginalObjectKey),
		fileExtension(doc.Name),
	)
	if payload.CorrelationID == "" {
		payload.CorrelationID = correlationFromDoc(doc)
	}
	if strings.TrimSpace(payload.OriginalBucket) == "" || strings.TrimSpace(payload.OriginalObjectKey) == "" {
		return workersvc.JobResult{Payload: payload.ToMap()}, fmt.Errorf("preprocess original location is required")
	}
	if strings.TrimSpace(payload.TargetBucket) == "" || strings.TrimSpace(payload.TargetObjectKey) == "" {
		return workersvc.JobResult{Payload: payload.ToMap()}, fmt.Errorf("preprocess target location is required")
	}

	jobCtx := w.jobContext(ctx, payload.CorrelationID)
	if payload.PreprocessJobID == "" {
		result, submitErr := w.activities.SubmitPreprocess(jobCtx, preprocessInput{
			JobID:           payload.PreprocessJobID,
			DocumentID:      job.DocumentID,
			SourceBucket:    payload.OriginalBucket,
			SourceObjectKey: payload.OriginalObjectKey,
			SourceETag:      payload.OriginalETag,
			TargetBucket:    payload.TargetBucket,
			TargetObjectKey: payload.TargetObjectKey,
			CorrelationID:   payload.CorrelationID,
		})
		if submitErr != nil {
			if isTemporaryGRPCError(submitErr) {
				payload.LastError = submitErr.Error()
				return w.postponeWithBackoff(ctx, job, payload.ToMap(), submitErr)
			}
			return workersvc.JobResult{Payload: payload.ToMap()}, submitErr
		}
		payload.PreprocessJobID = result.JobID
		payload.PreprocessStatus = result.Status.String()
		payload.PreprocessErrorMessage = result.ErrorMessage
		payload.PreprocessedBucket = firstNonEmpty(payload.PreprocessedBucket, result.TargetBucket, payload.TargetBucket)
		payload.PreprocessedObjectKey = firstNonEmpty(payload.PreprocessedObjectKey, result.TargetObjectKey, payload.TargetObjectKey)
		payload.CorrelationID = firstNonEmpty(result.CorrelationID, payload.CorrelationID)
		switch result.Status {
		case preprocessv1.PreprocessStatus_PREPROCESS_STATUS_COMPLETED:
			return workersvc.JobResult{Payload: payload.ToMap(), Event: &domain.JobEvent{EventType: "completed"}}, nil
		case preprocessv1.PreprocessStatus_PREPROCESS_STATUS_FAILED:
			reason := preprocessFailureReason("", payload.PreprocessErrorMessage)
			payload.PreprocessErrorMessage = reason
			payload.LastError = reason
			return workersvc.JobResult{Payload: payload.ToMap(), Event: &domain.JobEvent{EventType: "failed", Message: reason}}, fmt.Errorf("%s", reason)
		}
		next := time.Now().Add(w.retryDelay)
		return workersvc.JobResult{
			Payload: payload.ToMap(),
			Status:  domain.JobStatusPending,
			NextRun: &next,
			Event:   &domain.JobEvent{EventType: "postponed", Message: "preprocess submitted"},
		}, nil
	}

	result, pollErr := w.activities.GetPreprocessStatus(jobCtx, payload.PreprocessJobID, payload.CorrelationID)
	if pollErr != nil {
		if isTemporaryGRPCError(pollErr) {
			payload.LastError = pollErr.Error()
			return w.postponeWithBackoff(ctx, job, payload.ToMap(), pollErr)
		}
		return workersvc.JobResult{Payload: payload.ToMap()}, pollErr
	}

	payload.PreprocessJobID = firstNonEmpty(payload.PreprocessJobID, result.JobID)
	payload.PreprocessStatus = result.Status.String()
	payload.PreprocessErrorMessage = result.ErrorMessage
	payload.PreprocessedBucket = firstNonEmpty(payload.PreprocessedBucket, result.TargetBucket, payload.TargetBucket)
	payload.PreprocessedObjectKey = firstNonEmpty(payload.PreprocessedObjectKey, result.TargetObjectKey, payload.TargetObjectKey)
	payload.CorrelationID = firstNonEmpty(result.CorrelationID, payload.CorrelationID)

	switch result.Status {
	case preprocessv1.PreprocessStatus_PREPROCESS_STATUS_COMPLETED:
		payload.PollAttempts = 0
		payload.PreprocessedBucket = firstNonEmpty(payload.PreprocessedBucket, result.TargetBucket, payload.TargetBucket)
		payload.PreprocessedObjectKey = firstNonEmpty(payload.PreprocessedObjectKey, result.TargetObjectKey, payload.TargetObjectKey)
		return workersvc.JobResult{Payload: payload.ToMap(), Event: &domain.JobEvent{EventType: "completed"}}, nil
	case preprocessv1.PreprocessStatus_PREPROCESS_STATUS_FAILED:
		reason := preprocessFailureReason(result.ErrorCode, firstNonEmpty(result.ErrorMessage, payload.PreprocessErrorMessage))
		payload.PreprocessErrorMessage = reason
		payload.LastError = reason
		return workersvc.JobResult{Payload: payload.ToMap(), Event: &domain.JobEvent{EventType: "failed", Message: reason}}, fmt.Errorf("%s", reason)
	default:
		delay := w.preprocessBackoff(payload.PollAttempts)
		payload.PollAttempts++
		next := time.Now().Add(delay)
		return workersvc.JobResult{
			Payload: payload.ToMap(),
			Status:  domain.JobStatusPending,
			NextRun: &next,
			Event:   &domain.JobEvent{EventType: "postponed", Message: fmt.Sprintf("status=%s", result.Status.String())},
		}, nil
	}
}

func (w *StageWorker) handleExtractTables(ctx context.Context, job domain.ProcessingJob) (workersvc.JobResult, error) {
	payload := domain.ExtractTablesPayloadFromMap(job.Payload)
	if w.activities == nil {
		return workersvc.JobResult{Payload: payload.ToMap()}, errors.New("activities not configured")
	}

	doc, err := w.repo.GetDocument(ctx, job.DocumentID)
	if err != nil {
		return workersvc.JobResult{Payload: payload.ToMap()}, fmt.Errorf("load document: %w", err)
	}
	if doc == nil {
		return workersvc.JobResult{Payload: payload.ToMap()}, fmt.Errorf("document %s not found", job.DocumentID)
	}

	if tableQueueID := firstNonEmpty(payload.TableQueueID, payload.QueueID); tableQueueID != "" {
		payload.TableQueueID = tableQueueID
		payload.QueueID = tableQueueID
	}

	payload.OriginalBucket = firstNonEmpty(payload.OriginalBucket, doc.OriginalBucket)
	payload.OriginalObjectKey = firstNonEmpty(payload.OriginalObjectKey, doc.OriginalObjectKey)
	payload.ArtifactsBucket = firstNonEmpty(payload.ArtifactsBucket, doc.MarkdownBucket)
	payload.MarkdownBucket = firstNonEmpty(payload.MarkdownBucket, payload.ArtifactsBucket, doc.MarkdownBucket)
	payload.MarkdownObjectKey = firstNonEmpty(payload.MarkdownObjectKey, doc.MarkdownObjectKey, resolvedMarkdownKey(doc))
	payload.DoclingMarkdownBucket = firstNonEmpty(payload.DoclingMarkdownBucket, payload.MarkdownBucket, doc.MarkdownBucket)
	payload.DoclingMarkdownObjectKey = firstNonEmpty(payload.DoclingMarkdownObjectKey, payload.MarkdownObjectKey, doc.MarkdownObjectKey, resolvedMarkdownKey(doc))
	if payload.ConversionStrategy == "" {
		payload.ConversionStrategy = doc.ConversionStrategy
	} else {
		payload.ConversionStrategy = domain.NormalizeConversionStrategy(string(payload.ConversionStrategy))
	}
	if !payload.ExtractTables {
		payload.ExtractTables = doc.ExtractTables || payload.ConversionStrategy == domain.ConversionStrategyDoclingWithTables
	}
	prefix, prefixErr := resolveArtifactsPrefix(payload.ArtifactsPrefixTemplate, doc.ID, payload.TableQueueID, payload.ArtifactsPrefix, payload.MarkdownObjectKey)
	if prefixErr != nil {
		payload.LastError = prefixErr.Error()
		return workersvc.JobResult{Payload: payload.ToMap()}, prefixErr
	}
	payload.ArtifactsPrefix = prefix
	if payload.CorrelationID == "" {
		payload.CorrelationID = correlationFromDoc(doc)
	}

	if strings.TrimSpace(payload.OriginalBucket) == "" || strings.TrimSpace(payload.OriginalObjectKey) == "" {
		return workersvc.JobResult{Payload: payload.ToMap()}, fmt.Errorf("table extraction original location is required")
	}
	if strings.TrimSpace(payload.ArtifactsBucket) == "" {
		return workersvc.JobResult{Payload: payload.ToMap()}, fmt.Errorf("artifacts bucket is required for table extraction")
	}

	jobCtx := w.jobContext(ctx, payload.CorrelationID)
	if payload.TableQueueID == "" {
		result, submitErr := w.activities.ExtractTables(jobCtx, tableExtractionInput{
			DocumentID:              job.DocumentID,
			OriginalBucket:          payload.OriginalBucket,
			OriginalObjectKey:       payload.OriginalObjectKey,
			ArtifactsBucket:         payload.ArtifactsBucket,
			ArtifactsPrefix:         payload.ArtifactsPrefix,
			ArtifactsPrefixTemplate: payload.ArtifactsPrefixTemplate,
			MarkdownBucket:          payload.MarkdownBucket,
			MarkdownObjectKey:       payload.MarkdownObjectKey,
			CorrelationID:           payload.CorrelationID,
		})
		if submitErr != nil {
			if isTemporaryGRPCError(submitErr) {
				payload.LastError = submitErr.Error()
				return w.postponeWithBackoff(ctx, job, payload.ToMap(), submitErr)
			}
			return workersvc.JobResult{Payload: payload.ToMap()}, submitErr
		}
		return w.processTableExtractionResult(jobCtx, job, doc, payload, result)
	}

	result, pollErr := w.activities.GetTableExtractionJob(jobCtx, payload.TableQueueID, payload.CorrelationID)
	if pollErr != nil {
		if isTemporaryGRPCError(pollErr) {
			payload.LastError = pollErr.Error()
			return w.postponeWithBackoff(ctx, job, payload.ToMap(), pollErr)
		}
		return workersvc.JobResult{Payload: payload.ToMap()}, pollErr
	}

	return w.processTableExtractionResult(jobCtx, job, doc, payload, result)
}

func (w *StageWorker) handleConvert(ctx context.Context, job domain.ProcessingJob) (workersvc.JobResult, error) {
	payload := domain.ConvertPayloadFromMap(job.Payload)
	if w.activities == nil {
		return workersvc.JobResult{Payload: payload.ToMap()}, errors.New("activities not configured")
	}

	doc, err := w.repo.GetDocument(ctx, job.DocumentID)
	if err != nil {
		return workersvc.JobResult{Payload: payload.ToMap()}, fmt.Errorf("load document: %w", err)
	}
	if doc == nil {
		return workersvc.JobResult{Payload: payload.ToMap()}, fmt.Errorf("document %s not found", job.DocumentID)
	}

	payload.OriginalBucket = firstNonEmpty(payload.OriginalBucket, doc.OriginalBucket)
	payload.OriginalObjectKey = firstNonEmpty(payload.OriginalObjectKey, doc.OriginalObjectKey)
	payload.MarkdownBucket = firstNonEmpty(payload.MarkdownBucket, doc.MarkdownBucket)
	payload.MarkdownObjectKey = firstNonEmpty(payload.MarkdownObjectKey, resolvedMarkdownKey(doc))
	payload.DoclingMarkdownBucket = firstNonEmpty(payload.DoclingMarkdownBucket, payload.MarkdownBucket, doc.MarkdownBucket)
	payload.DoclingMarkdownObjectKey = firstNonEmpty(payload.DoclingMarkdownObjectKey, payload.MarkdownObjectKey, resolvedMarkdownKey(doc))
	preprocessedExtension := firstNonEmpty(
		fileExtension(payload.PreprocessedObjectKey),
		fileExtension(payload.TargetObjectKey),
	)
	payload.FilenameExtension = firstNonEmpty(
		preprocessedExtension,
		payload.FilenameExtension,
		fileExtension(payload.OriginalObjectKey),
		fileExtension(doc.OriginalObjectKey),
		fileExtension(doc.Name),
	)
	if payload.ConversionStrategy == "" {
		payload.ConversionStrategy = doc.ConversionStrategy
	} else {
		payload.ConversionStrategy = domain.NormalizeConversionStrategy(string(payload.ConversionStrategy))
	}
	payload.ExtractTables = payload.ExtractTables || doc.ExtractTables || payload.ConversionStrategy == domain.ConversionStrategyTablesOnly
	if payload.CorrelationID == "" {
		payload.CorrelationID = correlationFromDoc(doc)
	}

	sourceBucket := payload.OriginalBucket
	sourceObjectKey := payload.OriginalObjectKey
	if payload.PreprocessedBucket != "" && payload.PreprocessedObjectKey != "" {
		sourceBucket = payload.PreprocessedBucket
		sourceObjectKey = payload.PreprocessedObjectKey
	} else {
		sourceBucket = firstNonEmpty(payload.PreprocessedBucket, payload.OriginalBucket)
		sourceObjectKey = firstNonEmpty(payload.PreprocessedObjectKey, payload.TargetObjectKey, payload.OriginalObjectKey)
	}

	input := convertInput{
		DocumentID:        job.DocumentID,
		OriginalBucket:    sourceBucket,
		MarkdownBucket:    payload.MarkdownBucket,
		OriginalObjectKey: sourceObjectKey,
		MarkdownObjectKey: payload.MarkdownObjectKey,
		FilenameExtension: payload.FilenameExtension,
		Version:           latestVersion(doc).Version + 1,
	}

	convCtx := w.jobContext(ctx, payload.CorrelationID)
	result, err := w.activities.ConvertToMarkdown(convCtx, input)
	if err != nil {
		if isTemporaryGRPCError(err) {
			payload.LastError = err.Error()
			return w.postponeWithBackoff(ctx, job, payload.ToMap(), err)
		}
		return workersvc.JobResult{Payload: payload.ToMap()}, err
	}

	payload.MarkdownObjectKey = result.MarkdownKey
	payload.DoclingMarkdownBucket = firstNonEmpty(payload.DoclingMarkdownBucket, payload.MarkdownBucket)
	payload.DoclingMarkdownObjectKey = firstNonEmpty(payload.DoclingMarkdownObjectKey, payload.MarkdownObjectKey)
	if queueID := strings.TrimSpace(result.QueueID); queueID != "" {
		payload.QueueID = queueID
	}
	if status := strings.TrimSpace(result.Status); status != "" {
		payload.Status = status
		payload.DoclingStatus = status
	}
	payload.TableMetadata = payload.TableMetadata.Merge(result.TableMetadata)
	if result.TableCount > 0 || len(result.TablePages) > 0 {
		payload.TableMetadata = payload.TableMetadata.Merge(domain.TableMetadata{
			TablesCount: int32(result.TableCount),
			Pages:       result.TablePages,
		})
	}
	if result.Ready && result.TableCount == 0 && len(result.TablePages) == 0 && result.TableMetadata.IsEmpty() {
		payload.TableMetadata = domain.TableMetadata{}
		payload.TablePages = nil
		payload.TableCount = 0
	}
	if len(result.TablePages) > 0 {
		payload.TablePages = append([]int32(nil), result.TablePages...)
	}

	count, pages := payload.TableInfo()
	if count > 0 {
		payload.TableCount = int32(count)
		payload.TablePages = pages
	} else {
		payload.TableCount = 0
		payload.TablePages = nil
	}
	if payload.DoclingMarkdownBucket == "" {
		payload.DoclingMarkdownBucket = payload.MarkdownBucket
	}
	if payload.DoclingMarkdownObjectKey == "" {
		payload.DoclingMarkdownObjectKey = payload.MarkdownObjectKey
	}
	if payload.HasMarkdownTablesHint() {
		payload.ExtractTables = true
	}
	shouldExtractTables := w.shouldExtractAfterConvert(doc, payload, count, pages)
	requiresTablesChain := shouldExtractTables

	if !result.Ready {
		next := time.Now().Add(w.retryDelay)
		return workersvc.JobResult{
			Payload: payload.ToMap(),
			NextRun: &next,
			Status:  domain.JobStatusPending,
			Event:   &domain.JobEvent{EventType: "postponed", Message: fmt.Sprintf("status=%s", result.Status)},
		}, nil
	}

	eventPayload := map[string]any{}
	if sourceBucket != "" {
		eventPayload["source_bucket"] = sourceBucket
	}
	if sourceObjectKey != "" {
		eventPayload["source_object_key"] = sourceObjectKey
	}
	if payload.PreprocessJobID != "" {
		eventPayload["preprocess_job_id"] = payload.PreprocessJobID
	}
	if payload.DoclingMarkdownBucket != "" {
		eventPayload["docling_markdown_bucket"] = payload.DoclingMarkdownBucket
	}
	if payload.DoclingMarkdownObjectKey != "" {
		eventPayload["docling_markdown_object_key"] = payload.DoclingMarkdownObjectKey
	}
	if payload.DoclingStatus != "" {
		eventPayload["docling_status"] = payload.DoclingStatus
	}
	if len(eventPayload) == 0 {
		eventPayload = nil
	}

	if requiresTablesChain {
		return workersvc.JobResult{Payload: payload.ToMap(), Event: &domain.JobEvent{EventType: "completed", Payload: eventPayload}}, nil
	}

	if err := w.activities.MarkConverted(convCtx, doc, result.MarkdownKey, sourceObjectKey, result.QueueID, result.Version); err != nil {
		return workersvc.JobResult{Payload: payload.ToMap()}, fmt.Errorf("update status: %w", err)
	}

	return workersvc.JobResult{Payload: payload.ToMap(), Event: &domain.JobEvent{EventType: "completed", Payload: eventPayload}}, nil
}

func (w *StageWorker) processTableExtractionResult(ctx context.Context, job domain.ProcessingJob, doc *domain.Document, payload domain.ExtractTablesPayload, result tableExtractionResult) (workersvc.JobResult, error) {
	initialMarkdownBucket := payload.MarkdownBucket
	initialMarkdownObjectKey := payload.MarkdownObjectKey
	doclingBucket := firstNonEmpty(payload.DoclingMarkdownBucket, initialMarkdownBucket, doc.MarkdownBucket)
	doclingObjectKey := firstNonEmpty(payload.DoclingMarkdownObjectKey, initialMarkdownObjectKey, doc.MarkdownObjectKey, resolvedMarkdownKey(doc))

	payload.TableQueueID = firstNonEmpty(result.QueueID, payload.TableQueueID, payload.QueueID)
	payload.QueueID = payload.TableQueueID
	payload.Status = result.Status.String()
	payload.StatusReason = firstNonEmpty(result.StatusReason, payload.StatusReason)
	payload.ArtifactsBucket = firstNonEmpty(result.ArtifactsBucket, payload.ArtifactsBucket)
	prefix, prefixErr := resolveArtifactsPrefix(payload.ArtifactsPrefixTemplate, doc.ID, payload.TableQueueID, firstNonEmpty(result.ArtifactsPrefix, payload.ArtifactsPrefix), payload.MarkdownObjectKey)
	if prefixErr != nil {
		payload.LastError = prefixErr.Error()
		return workersvc.JobResult{Payload: payload.ToMap()}, prefixErr
	}
	payload.ArtifactsPrefix = prefix
	payload.MarkdownObjectKey = firstNonEmpty(result.MarkdownObjectKey, payload.MarkdownObjectKey, tablesMarkdownKey(payload.ArtifactsPrefix))
	payload.MarkdownBucket = firstNonEmpty(result.MarkdownBucket, payload.MarkdownBucket, payload.ArtifactsBucket, doc.MarkdownBucket)
	payload.CorrelationID = firstNonEmpty(result.CorrelationID, payload.CorrelationID)
	if payload.ConversionStrategy == "" {
		payload.ConversionStrategy = doc.ConversionStrategy
	} else {
		payload.ConversionStrategy = domain.NormalizeConversionStrategy(string(payload.ConversionStrategy))
	}
	if !payload.ExtractTables {
		payload.ExtractTables = doc.ExtractTables
	}
	if payload.ConversionStrategy == domain.ConversionStrategyDoclingWithTables {
		payload.ExtractTables = true
	}
	payload.DoclingMarkdownBucket = doclingBucket
	payload.DoclingMarkdownObjectKey = firstNonEmpty(doclingObjectKey, resolvedMarkdownKey(doc))

	if payload.TableQueueID == "" {
		err := fmt.Errorf("table extraction queue_id is empty for document %s", job.DocumentID)
		payload.LastError = err.Error()
		return workersvc.JobResult{Payload: payload.ToMap()}, err
	}

	switch result.Status {
	case tableextractorv1.TableExtractionStatus_TABLE_EXTRACTION_STATUS_DONE:
		payload.PollAttempts = 0
		return w.completeTableExtraction(ctx, doc, payload)
	case tableextractorv1.TableExtractionStatus_TABLE_EXTRACTION_STATUS_FAILED:
		reason := firstNonEmpty(payload.StatusReason, "table extraction failed")
		payload.StatusReason = reason
		payload.LastError = reason
		return workersvc.JobResult{Payload: payload.ToMap(), Event: &domain.JobEvent{EventType: "failed", Message: reason}}, fmt.Errorf("%s", reason)
	case tableextractorv1.TableExtractionStatus_TABLE_EXTRACTION_STATUS_UNSPECIFIED:
		err := fmt.Errorf("table extraction status is empty for queue_id=%s", payload.QueueID)
		payload.LastError = err.Error()
		return workersvc.JobResult{Payload: payload.ToMap()}, err
	default:
		delay := w.preprocessBackoff(payload.PollAttempts)
		payload.PollAttempts++
		next := time.Now().Add(delay)
		return workersvc.JobResult{
			Payload: payload.ToMap(),
			Status:  domain.JobStatusPending,
			NextRun: &next,
			Event:   &domain.JobEvent{EventType: "postponed", Message: fmt.Sprintf("status=%s", result.Status.String())},
		}, nil
	}
}

func (w *StageWorker) completeTableExtraction(ctx context.Context, doc *domain.Document, payload domain.ExtractTablesPayload) (workersvc.JobResult, error) {
	if doc == nil {
		return workersvc.JobResult{Payload: payload.ToMap()}, errors.New("document is required")
	}
	tableMarkdownKey := payload.MarkdownObjectKey
	if tableMarkdownKey == "" {
		tableMarkdownKey = tablesMarkdownKey(payload.ArtifactsPrefix)
	}
	if tableMarkdownKey == "" {
		err := errors.New("table extraction markdown_object_key is empty")
		payload.LastError = err.Error()
		return workersvc.JobResult{Payload: payload.ToMap()}, err
	}

	targetBucket := firstNonEmpty(doc.MarkdownBucket, payload.MarkdownBucket)
	targetKey := resolvedMarkdownKey(doc)
	finalMarkdownKey := tableMarkdownKey
	baseBucket := strings.TrimSpace(firstNonEmpty(payload.DoclingMarkdownBucket, payload.MarkdownBucket, doc.MarkdownBucket))
	baseKey := strings.TrimSpace(firstNonEmpty(payload.DoclingMarkdownObjectKey, resolvedMarkdownKey(doc)))

	if baseBucket == "" || baseKey == "" {
		err := errors.New("base markdown is required to merge extracted tables")
		payload.LastError = err.Error()
		return workersvc.JobResult{Payload: payload.ToMap()}, err
	}
	if targetBucket == "" || targetKey == "" {
		err := errors.New("target markdown location is required to merge extracted tables")
		payload.LastError = err.Error()
		return workersvc.JobResult{Payload: payload.ToMap()}, err
	}
	if w.activities == nil {
		err := errors.New("activities not configured")
		payload.LastError = err.Error()
		return workersvc.JobResult{Payload: payload.ToMap()}, err
	}

	mergedKey, mergeErr := w.activities.MergeTables(ctx, mergeTablesInput{
		Doc:             doc,
		BaseBucket:      baseBucket,
		BaseObjectKey:   baseKey,
		TablesBucket:    firstNonEmpty(payload.MarkdownBucket, payload.ArtifactsBucket, targetBucket),
		TablesObjectKey: tableMarkdownKey,
		TargetBucket:    targetBucket,
		TargetObjectKey: targetKey,
		CorrelationID:   payload.CorrelationID,
	})
	if mergeErr != nil {
		payload.LastError = mergeErr.Error()
		return workersvc.JobResult{Payload: payload.ToMap()}, mergeErr
	}
	finalMarkdownKey = mergedKey
	payload.MarkdownObjectKey = mergedKey

	version := latestVersion(doc).Version + 1
	if err := w.activities.MarkConverted(ctx, doc, finalMarkdownKey, payload.OriginalObjectKey, payload.TableQueueID, version); err != nil {
		return workersvc.JobResult{Payload: payload.ToMap()}, fmt.Errorf("update status: %w", err)
	}

	eventPayload := map[string]any{}
	if payload.ArtifactsBucket != "" {
		eventPayload["artifacts_bucket"] = payload.ArtifactsBucket
	}
	if payload.ArtifactsPrefix != "" {
		eventPayload["artifacts_prefix"] = payload.ArtifactsPrefix
	}
	if payload.TableQueueID != "" {
		eventPayload["table_queue_id"] = payload.TableQueueID
	}
	if tableMarkdownKey != "" {
		eventPayload["tables_markdown_object_key"] = tableMarkdownKey
	}
	if payload.DoclingMarkdownObjectKey != "" {
		eventPayload["docling_markdown_object_key"] = payload.DoclingMarkdownObjectKey
	}
	if finalMarkdownKey != "" {
		eventPayload["markdown_object_key"] = finalMarkdownKey
	}

	return workersvc.JobResult{Payload: payload.ToMap(), Event: &domain.JobEvent{EventType: "completed", Payload: eventPayload}}, nil
}

func (w *StageWorker) handlePublish(ctx context.Context, job domain.ProcessingJob) (workersvc.JobResult, error) {
	payload := domain.PublishPayloadFromMap(job.Payload)
	if w.activities == nil {
		return workersvc.JobResult{Payload: payload.ToMap()}, errors.New("activities not configured")
	}

	doc, err := w.repo.GetDocument(ctx, job.DocumentID)
	if err != nil {
		return workersvc.JobResult{Payload: payload.ToMap()}, fmt.Errorf("load document: %w", err)
	}
	if doc == nil {
		return workersvc.JobResult{Payload: payload.ToMap()}, fmt.Errorf("document %s not found", job.DocumentID)
	}

	payload.Repository = firstNonEmpty(payload.Repository, fmt.Sprintf("%s/%s", doc.Owner, doc.Repository))
	owner, repository, _ := strings.Cut(payload.Repository, "/")
	if repository == "" {
		repository = payload.Repository
	}

	payload.MarkdownBucket = firstNonEmpty(payload.MarkdownBucket, doc.MarkdownBucket)
	payload.MarkdownObjectKey = firstNonEmpty(payload.MarkdownObjectKey, resolvedMarkdownKey(doc))
	payload.Branch = firstNonEmpty(payload.Branch, doc.Branch)
	payload.BaseBranch = firstNonEmpty(payload.BaseBranch, doc.BaseBranch)
	payload.GitHubPath = firstNonEmpty(payload.GitHubPath, doc.Path)
	if payload.CreatePR == nil {
		payload.CreatePR = boolPtr(doc.CreatePR)
	}
	if payload.CommitMessage == "" {
		payload.CommitMessage = pickCommitMessage(doc, PublishDefaults{})
	}
	if payload.PRTitle == "" {
		payload.PRTitle = pickPRTitle(doc, PublishDefaults{})
	}
	if payload.CorrelationID == "" {
		payload.CorrelationID = correlationFromDoc(doc)
	}

	input := publishInput{
		DocumentID:        job.DocumentID,
		DocumentName:      doc.Name,
		Owner:             owner,
		Repository:        repository,
		Branch:            payload.Branch,
		BaseBranch:        payload.BaseBranch,
		Path:              payload.GitHubPath,
		CreatePR:          derefBool(payload.CreatePR),
		MarkdownBucket:    payload.MarkdownBucket,
		MarkdownObjectKey: payload.MarkdownObjectKey,
		CorrelationID:     payload.CorrelationID,
		CommitMessage:     payload.CommitMessage,
		AuthorName:        payload.AuthorName,
		AuthorEmail:       payload.AuthorEmail,
		PRTitle:           payload.PRTitle,
		PRBody:            payload.PRBody,
	}

	if strings.TrimSpace(input.CommitMessage) == "" {
		return workersvc.JobResult{Payload: payload.ToMap()}, errors.New("commit_message is required for publish")
	}
	if strings.TrimSpace(input.AuthorName) == "" || strings.TrimSpace(input.AuthorEmail) == "" {
		return workersvc.JobResult{Payload: payload.ToMap()}, errors.New("author_name and author_email are required for publish")
	}
	if input.MarkdownObjectKey != "" {
		input.IdempotencyKey = fmt.Sprintf("publish:%s:%s:%s", job.DocumentID, input.Branch, input.MarkdownObjectKey)
	}

	payload.Repository = fmt.Sprintf("%s/%s", owner, repository)
	payload.Branch = input.Branch
	payload.BaseBranch = input.BaseBranch
	payload.GitHubPath = input.Path
	payload.MarkdownBucket = input.MarkdownBucket
	payload.MarkdownObjectKey = input.MarkdownObjectKey
	payload.CommitMessage = input.CommitMessage
	payload.PRTitle = input.PRTitle
	payload.PRBody = input.PRBody
	payload.AuthorName = input.AuthorName
	payload.AuthorEmail = input.AuthorEmail
	payload.CreatePR = boolPtr(input.CreatePR)
	payload.CorrelationID = input.CorrelationID

	resp, err := w.activities.PublishMarkdown(w.jobContext(ctx, payload.CorrelationID), input)
	eventPayload := buildPublishEventPayload(resp)
	result := workersvc.JobResult{Payload: payload.ToMap(), Event: &domain.JobEvent{Payload: eventPayload, EventType: "completed"}}
	if err != nil {
		if isTemporaryGRPCError(err) {
			payload.LastError = err.Error()
			return w.postponeWithBackoff(ctx, job, payload.ToMap(), err)
		}
		result.Event.EventType = "failed"
		result.Event.Message = err.Error()
		if retryAfter := pickRetryAfter(resp); retryAfter != nil && retryAfter.AsDuration() > 0 {
			next := time.Now().Add(retryAfter.AsDuration())
			result.NextRun = &next
			if result.Event.Payload == nil {
				result.Event.Payload = map[string]any{}
			}
			result.Event.Payload["retry_after"] = retryAfter.AsDuration().String()
			payload.RetryAfter = retryAfter.AsDuration().String()
			result.Payload = payload.ToMap()
		}
		return result, err
	}

	return result, nil
}

func (w *StageWorker) postponeWithBackoff(ctx context.Context, job domain.ProcessingJob, payload map[string]any, err error) (workersvc.JobResult, error) {
	if job.Attempts >= w.maxAttempts {
		if w.activities != nil && w.activities.statuses != nil {
			_ = w.activities.statuses.Fail(ctx, job.DocumentID, fmt.Sprintf("max attempts exceeded: %v", err))
		}
		payload["last_error"] = err.Error()
		return workersvc.JobResult{Payload: payload}, fmt.Errorf("max attempts exceeded for job %s: %w", job.TaskID, err)
	}

	delay := w.retryDelay * time.Duration(1<<job.Attempts)
	next := time.Now().Add(delay)
	payload["last_error"] = err.Error()

	return workersvc.JobResult{
		Payload:          payload,
		Status:           domain.JobStatusPending,
		NextRun:          &next,
		IncrementAttempt: true,
		Event:            &domain.JobEvent{EventType: "postponed", Message: err.Error()},
	}, nil
}

func (w *StageWorker) preprocessBackoff(attempts int) time.Duration {
	delay := w.retryDelay * time.Duration(1<<attempts)
	maxDelay := w.retryDelay * 16
	if delay > maxDelay {
		delay = maxDelay
	}
	if delay <= 0 {
		delay = w.retryDelay
	}
	return delay
}

func buildPublishEventPayload(resp *githubpublisherv1.PublishMarkdownResponse) map[string]any {
	if resp == nil {
		return nil
	}
	payload := map[string]any{}
	if sha := strings.TrimSpace(resp.GetCommitSha()); sha != "" {
		payload["commit_sha"] = sha
	}
	if resp.GetPullRequestNumber() > 0 {
		payload["pull_request_number"] = resp.GetPullRequestNumber()
	}
	if url := strings.TrimSpace(resp.GetPullRequestUrl()); url != "" {
		payload["pull_request_url"] = url
	}
	if resp.GetIdempotencyHit() {
		payload["idempotency_hit"] = true
	}
	if retry := resp.GetRetryAfter(); retry != nil && retry.AsDuration() > 0 {
		payload["retry_after"] = retry.AsDuration().String()
	}
	if len(payload) == 0 {
		return nil
	}
	return payload
}

func pickRetryAfter(resp *githubpublisherv1.PublishMarkdownResponse) *durationpb.Duration {
	if resp == nil {
		return nil
	}
	return resp.GetRetryAfter()
}

func (w *StageWorker) shouldExtractAfterConvert(doc *domain.Document, payload domain.ConvertPayload, tableCount int, tablePages []int32) bool {
	if doc == nil {
		return false
	}
	if w.router == nil {
		return payload.ExtractTables || payload.ConversionStrategy == domain.ConversionStrategyTablesOnly
	}
	docCopy := *doc
	if payload.ConversionStrategy != "" {
		docCopy.ConversionStrategy = payload.ConversionStrategy
	}
	return w.router.ShouldExtractTablesAfterConvert(&docCopy, payload.ExtractTables, tableCount, tablePages, payload.HasMarkdownTablesHint())
}

func (w *StageWorker) jobContext(ctx context.Context, correlationID string) context.Context {
	corr := strings.TrimSpace(correlationID)
	if corr == "" {
		corr = correlationIDFromContext(ctx)
	}
	if corr == "" {
		return ctx
	}
	return metadataAppend(ctx, corr)
}

func metadataAppend(ctx context.Context, correlationID string) context.Context {
	return metadata.AppendToOutgoingContext(ctx, correlationIDHeader, correlationID)
}

type artifactsPrefixTemplateData struct {
	DocumentID string
	QueueID    string
}

func resolveArtifactsPrefix(templateStr, documentID, queueID, existingPrefix, markdownKey string) (string, error) {
	trimmedTemplate := strings.TrimSpace(templateStr)
	if trimmedTemplate != "" && queueID != "" {
		return renderArtifactsPrefixTemplate(trimmedTemplate, artifactsPrefixTemplateData{DocumentID: documentID, QueueID: queueID})
	}
	if strings.TrimSpace(existingPrefix) != "" {
		return existingPrefix, nil
	}
	if trimmedTemplate != "" {
		if markdownKey != "" {
			return tablesArtifactsPrefix(markdownKey), nil
		}
		return "", nil
	}
	if markdownKey != "" {
		return tablesArtifactsPrefix(markdownKey), nil
	}
	return "", nil
}

func renderArtifactsPrefixTemplate(tmpl string, data artifactsPrefixTemplateData) (string, error) {
	parsed, err := template.New("artifacts_prefix").Parse(tmpl)
	if err != nil {
		return "", err
	}
	var buf bytes.Buffer
	if err := parsed.Execute(&buf, data); err != nil {
		return "", err
	}
	prefix := strings.TrimSpace(buf.String())
	if prefix == "" {
		return "", errors.New("artifacts prefix template produced empty value")
	}
	return prefix, nil
}

func tablesArtifactsPrefix(markdownKey string) string {
	key := strings.TrimSpace(markdownKey)
	if key == "" {
		return ""
	}
	key = strings.TrimSuffix(key, "/")
	if key == "" {
		return ""
	}
	withoutExt := strings.TrimSuffix(key, path.Ext(key))
	if withoutExt == "" {
		return ""
	}
	return withoutExt
}

func tablesMarkdownKey(prefix string) string {
	cleanPrefix := strings.TrimSpace(prefix)
	if cleanPrefix == "" {
		return ""
	}
	cleanPrefix = strings.TrimSuffix(cleanPrefix, "/")
	if cleanPrefix == "" {
		return ""
	}
	return path.Join(cleanPrefix, "tables.md")
}

func preprocessTargetKey(originalKey string) string {
	key := strings.TrimSpace(originalKey)
	if key == "" {
		return ""
	}
	ext := path.Ext(key)
	base := strings.TrimSuffix(key, ext)
	if base == "" {
		base = key
	}
	if strings.EqualFold(ext, ".docx") {
		return key
	}
	return base + ".docx"
}

func fileExtension(name string) string {
	ext := path.Ext(name)
	ext = strings.TrimPrefix(ext, ".")
	return strings.ToLower(ext)
}

func firstNonEmpty(values ...string) string {
	for _, val := range values {
		if strings.TrimSpace(val) != "" {
			return val
		}
	}
	return ""
}

func boolPtr(val bool) *bool { return &val }

func derefBool(val *bool) bool {
	if val == nil {
		return false
	}
	return *val
}
