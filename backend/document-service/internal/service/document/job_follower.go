package document

import (
	"context"
	"fmt"
	"strings"
	"time"

	"backend-document-service/internal/domain"
)

type jobFollowerRepo interface {
	ListFinishedJobs(ctx context.Context, limit int, postpone time.Duration) ([]domain.ProcessingJob, error)
	GetDocument(ctx context.Context, id string) (*domain.Document, error)
	CreateProcessingJob(ctx context.Context, job *domain.ProcessingJob, event *domain.JobEvent) (*domain.ProcessingJob, error)
	UpdateStatus(ctx context.Context, id string, to domain.DocumentStatus, reason, errorReason string, version *domain.DocumentVersion) (*domain.Document, error)
	UpdateProcessingJob(ctx context.Context, taskID string, status domain.JobStatus, nextRunAt *time.Time, incrementAttempt bool, payload map[string]any, event *domain.JobEvent) (*domain.ProcessingJob, error)
}

type PublishDefaults struct {
	CommitMessage string
	PRTitle       string
	PRBody        string
	AuthorName    string
	AuthorEmail   string
}

// JobFollower читает завершённые задачи из processing_jobs и
// запускает следующую стадию либо помечает документ как Failed.
type JobFollower struct {
	repo      jobFollowerRepo
	batchSize int
	postpone  time.Duration
	metrics   *workerMetrics
	defaults  PublishDefaults
	router    *domain.StageRouter
}

func NewJobFollower(repo jobFollowerRepo, batchSize int, postpone time.Duration, defaults PublishDefaults, router *domain.StageRouter) *JobFollower {
	if batchSize <= 0 {
		batchSize = 100
	}
	if postpone <= 0 {
		postpone = 5 * time.Minute
	}
	if router == nil {
		router = domain.NewStageRouter(domain.RouterRules{})
	}

	return &JobFollower{
		repo:      repo,
		batchSize: batchSize,
		postpone:  postpone,
		metrics:   newWorkerMetrics("job_follower"),
		defaults:  defaults,
		router:    router,
	}
}

func (f *JobFollower) Run(ctx context.Context, interval time.Duration) error {
	if interval <= 0 {
		interval = time.Minute
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	if err := f.reconcile(ctx); err != nil {
		return err
	}

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if err := f.reconcile(ctx); err != nil {
				return err
			}
		}
	}
}

func (f *JobFollower) reconcile(ctx context.Context) error {
	for {
		jobs, err := f.repo.ListFinishedJobs(ctx, f.batchSize, f.postpone)
		if err != nil {
			f.metrics.recordError(ctx)
			return err
		}
		f.metrics.observeBatch(ctx, len(jobs))
		if len(jobs) == 0 {
			f.metrics.observeBatch(ctx, 0)
			return nil
		}

		for _, job := range jobs {
			if err := f.handleJob(ctx, job); err != nil {
				return err
			}
		}

		if len(jobs) < f.batchSize {
			return nil
		}
	}
}

func (f *JobFollower) handleJob(ctx context.Context, job domain.ProcessingJob) error {
	start := time.Now()
	defer f.metrics.recordDuration(ctx, time.Since(start))

	doc, err := f.repo.GetDocument(ctx, job.DocumentID)
	if err != nil {
		f.metrics.recordError(ctx)
		return err
	}
	if doc == nil {
		f.metrics.recordError(ctx)
		return fmt.Errorf("document %s not found for job %s", job.DocumentID, job.TaskID)
	}

	switch job.Status {
	case domain.JobStatusCompleted:
		return f.scheduleNext(ctx, job, doc)
	case domain.JobStatusFailed:
		return f.markFailed(ctx, job, doc)
	}

	return nil
}

func (f *JobFollower) scheduleNext(ctx context.Context, job domain.ProcessingJob, doc *domain.Document) error {
	if doc.Status == domain.DocumentStatusFailed {
		return nil
	}

	switch job.Stage {
	case domain.JobStageStoreOriginal:
		nextStage := f.router.NextStageAfterStore(doc)
		switch nextStage {
		case domain.JobStagePreprocessToDocx:
			originalETag := strings.TrimSpace(latestVersion(doc).OriginalETag)
			if originalETag == "" {
				originalETag = strings.TrimSpace(domain.StoreOriginalPayloadFromMap(job.Payload).OriginalETag)
			}
			if originalETag == "" {
				return fmt.Errorf("original etag is required to schedule preprocess for document %s", doc.ID)
			}

			payload := domain.PreprocessPayload{
				OriginalBucket:     doc.OriginalBucket,
				OriginalObjectKey:  doc.OriginalObjectKey,
				OriginalETag:       originalETag,
				FilenameExtension:  fileExtension(firstNonEmpty(doc.OriginalObjectKey, doc.Name)),
				JobPayloadMetadata: domain.JobPayloadMetadata{CorrelationID: correlationFromDoc(doc)},
			}.ToMap()
			event := &domain.JobEvent{EventType: "scheduled", Message: "next stage: preprocess"}
			if _, err := f.repo.CreateProcessingJob(ctx, &domain.ProcessingJob{DocumentID: doc.ID, Stage: domain.JobStagePreprocessToDocx, Status: domain.JobStatusPending, Payload: payload}, event); err != nil {
				return fmt.Errorf("enqueue preprocess for %s: %w", doc.ID, err)
			}
		case domain.JobStageExtractTables:
			payload := f.extractTablesPayloadForDocument(doc, correlationFromDoc(doc)).ToMap()
			event := &domain.JobEvent{EventType: "scheduled", Message: "next stage: extract_tables"}
			if _, err := f.repo.CreateProcessingJob(ctx, &domain.ProcessingJob{DocumentID: doc.ID, Stage: domain.JobStageExtractTables, Status: domain.JobStatusPending, Payload: payload}, event); err != nil {
				return fmt.Errorf("enqueue extract_tables for %s: %w", doc.ID, err)
			}
		case domain.JobStageConvertToMarkdown:
			payload := f.convertPayloadForDocument(doc, domain.ConvertPayload{}, correlationFromDoc(doc)).ToMap()
			event := &domain.JobEvent{EventType: "scheduled", Message: "next stage: convert"}
			if _, err := f.repo.CreateProcessingJob(ctx, &domain.ProcessingJob{DocumentID: doc.ID, Stage: domain.JobStageConvertToMarkdown, Status: domain.JobStatusPending, Payload: payload}, event); err != nil {
				return fmt.Errorf("enqueue convert for %s: %w", doc.ID, err)
			}
		default:
			return fmt.Errorf("unsupported next stage %s", nextStage)
		}
	case domain.JobStagePreprocessToDocx:
		overrides, correlationID := preprocessConversion(job.Payload, doc)
		payload := f.convertPayloadForDocument(doc, overrides, correlationID).ToMap()
		event := &domain.JobEvent{EventType: "scheduled", Message: "next stage: convert"}
		if _, err := f.repo.CreateProcessingJob(ctx, &domain.ProcessingJob{DocumentID: doc.ID, Stage: domain.JobStageConvertToMarkdown, Status: domain.JobStatusPending, Payload: payload}, event); err != nil {
			return fmt.Errorf("enqueue convert after preprocess for %s: %w", doc.ID, err)
		}
	case domain.JobStageConvertToMarkdown:
		convertPayload := domain.ConvertPayloadFromMap(job.Payload)
		if f.shouldExtractTablesAfterConvert(doc, convertPayload) {
			correlationID := correlationFromDoc(doc)
			if convertPayload.CorrelationID != "" {
				correlationID = convertPayload.CorrelationID
			}
			payload := f.extractTablesPayloadFromConvert(doc, convertPayload, correlationID).ToMap()
			event := &domain.JobEvent{EventType: "scheduled", Message: "next stage: extract_tables"}
			if _, err := f.repo.CreateProcessingJob(ctx, &domain.ProcessingJob{DocumentID: doc.ID, Stage: domain.JobStageExtractTables, Status: domain.JobStatusPending, Payload: payload}, event); err != nil {
				return fmt.Errorf("enqueue extract_tables for %s: %w", doc.ID, err)
			}
			return nil
		}

		payload := domain.PublishPayload{
			MarkdownBucket:     firstNonEmpty(convertPayload.MarkdownBucket, doc.MarkdownBucket),
			MarkdownObjectKey:  firstNonEmpty(convertPayload.MarkdownObjectKey, doc.MarkdownObjectKey, resolvedMarkdownKey(doc)),
			Repository:         fmt.Sprintf("%s/%s", doc.Owner, doc.Repository),
			Branch:             doc.Branch,
			BaseBranch:         doc.BaseBranch,
			GitHubPath:         doc.Path,
			CreatePR:           boolPtr(doc.CreatePR),
			CommitMessage:      pickCommitMessage(doc, f.defaults),
			PRTitle:            pickPRTitle(doc, f.defaults),
			PRBody:             f.defaults.PRBody,
			AuthorName:         f.defaults.AuthorName,
			AuthorEmail:        f.defaults.AuthorEmail,
			JobPayloadMetadata: domain.JobPayloadMetadata{CorrelationID: firstNonEmpty(convertPayload.CorrelationID, correlationFromDoc(doc))},
		}.ToMap()
		event := &domain.JobEvent{EventType: "scheduled", Message: "next stage: publish"}
		if _, err := f.repo.CreateProcessingJob(ctx, &domain.ProcessingJob{DocumentID: doc.ID, Stage: domain.JobStagePublishToGitHub, Status: domain.JobStatusPending, Payload: payload}, event); err != nil {
			return fmt.Errorf("enqueue publish for %s: %w", doc.ID, err)
		}
	case domain.JobStageExtractTables:
		payload := domain.PublishPayload{
			MarkdownBucket:     doc.MarkdownBucket,
			MarkdownObjectKey:  doc.MarkdownObjectKey,
			Repository:         fmt.Sprintf("%s/%s", doc.Owner, doc.Repository),
			Branch:             doc.Branch,
			BaseBranch:         doc.BaseBranch,
			GitHubPath:         doc.Path,
			CreatePR:           boolPtr(doc.CreatePR),
			CommitMessage:      pickCommitMessage(doc, f.defaults),
			PRTitle:            pickPRTitle(doc, f.defaults),
			PRBody:             f.defaults.PRBody,
			AuthorName:         f.defaults.AuthorName,
			AuthorEmail:        f.defaults.AuthorEmail,
			JobPayloadMetadata: domain.JobPayloadMetadata{CorrelationID: correlationFromDoc(doc)},
		}.ToMap()
		event := &domain.JobEvent{EventType: "scheduled", Message: "next stage: publish"}
		if _, err := f.repo.CreateProcessingJob(ctx, &domain.ProcessingJob{DocumentID: doc.ID, Stage: domain.JobStagePublishToGitHub, Status: domain.JobStatusPending, Payload: payload}, event); err != nil {
			return fmt.Errorf("enqueue publish for %s: %w", doc.ID, err)
		}
	}

	return nil
}

func (f *JobFollower) markFailed(ctx context.Context, job domain.ProcessingJob, doc *domain.Document) error {
	reason := fmt.Sprintf("stage %s failed", job.Stage)
	if lastError, ok := job.Payload["last_error"].(string); ok && lastError != "" {
		reason = fmt.Sprintf("%s: %s", reason, lastError)
	}

	if _, err := f.repo.UpdateStatus(ctx, doc.ID, domain.DocumentStatusFailed, reason, reason, nil); err != nil {
		return fmt.Errorf("mark failed document %s: %w", doc.ID, err)
	}

	if _, err := f.repo.UpdateProcessingJob(ctx, job.TaskID, job.Status, nil, false, job.Payload, &domain.JobEvent{EventType: "handled", Message: "marked document failed"}); err != nil {
		return fmt.Errorf("update failed job %s: %w", job.TaskID, err)
	}

	return nil
}

func pickCommitMessage(doc *domain.Document, defaults PublishDefaults) string {
	if defaults.CommitMessage != "" {
		return defaults.CommitMessage
	}
	if doc != nil && strings.TrimSpace(doc.Name) != "" {
		return fmt.Sprintf("Update %s", doc.Name)
	}
	return "Update document"
}

func pickPRTitle(doc *domain.Document, defaults PublishDefaults) string {
	if defaults.PRTitle != "" {
		return defaults.PRTitle
	}
	if doc != nil {
		return doc.Name
	}
	return ""
}

func correlationFromDoc(doc *domain.Document) string {
	if doc.WorkflowID != "" {
		return doc.WorkflowID
	}
	return doc.ID
}

func (f *JobFollower) extractTablesPayloadForDocument(doc *domain.Document, correlationID string) domain.ExtractTablesPayload {
	markdownKey := resolvedMarkdownKey(doc)
	config := f.router.TableExtractionConfig(doc)
	artifactsBucket := doc.MarkdownBucket
	if strings.TrimSpace(config.ArtifactsBucket) != "" {
		artifactsBucket = config.ArtifactsBucket
	}
	artifactsPrefix := tablesArtifactsPrefix(markdownKey)
	return domain.ExtractTablesPayload{
		OriginalBucket:           doc.OriginalBucket,
		OriginalObjectKey:        doc.OriginalObjectKey,
		ArtifactsBucket:          artifactsBucket,
		ArtifactsPrefix:          artifactsPrefix,
		ArtifactsPrefixTemplate:  config.ArtifactsPrefixTmpl,
		MarkdownObjectKey:        markdownKey,
		MarkdownBucket:           doc.MarkdownBucket,
		DoclingMarkdownBucket:    doc.MarkdownBucket,
		DoclingMarkdownObjectKey: markdownKey,
		ConversionStrategy:       doc.ConversionStrategy,
		ExtractTables:            doc.ExtractTables || doc.ConversionStrategy == domain.ConversionStrategyDoclingWithTables || doc.ConversionStrategy == domain.ConversionStrategyTablesOnly,
		JobPayloadMetadata:       domain.JobPayloadMetadata{CorrelationID: correlationID},
	}
}

func (f *JobFollower) convertPayloadForDocument(doc *domain.Document, overrides domain.ConvertPayload, correlationID string) domain.ConvertPayload {
	payload := domain.ConvertPayload{
		OriginalBucket:           firstNonEmpty(overrides.OriginalBucket, doc.OriginalBucket),
		OriginalObjectKey:        firstNonEmpty(overrides.OriginalObjectKey, doc.OriginalObjectKey),
		PreprocessJobID:          overrides.PreprocessJobID,
		PreprocessedBucket:       overrides.PreprocessedBucket,
		PreprocessedObjectKey:    overrides.PreprocessedObjectKey,
		TargetObjectKey:          overrides.TargetObjectKey,
		MarkdownBucket:           firstNonEmpty(overrides.MarkdownBucket, doc.MarkdownBucket),
		MarkdownObjectKey:        firstNonEmpty(overrides.MarkdownObjectKey, doc.MarkdownObjectKey),
		DoclingMarkdownBucket:    firstNonEmpty(overrides.DoclingMarkdownBucket, doc.MarkdownBucket),
		DoclingMarkdownObjectKey: firstNonEmpty(overrides.DoclingMarkdownObjectKey, doc.MarkdownObjectKey),
		DoclingStatus:            overrides.DoclingStatus,
		FilenameExtension:        overrides.FilenameExtension,
		JobPayloadMetadata:       domain.JobPayloadMetadata{CorrelationID: correlationID},
		ConversionStrategy:       doc.ConversionStrategy,
		ExtractTables:            doc.ExtractTables || doc.ConversionStrategy == domain.ConversionStrategyTablesOnly,
	}

	if payload.FilenameExtension == "" {
		payload.FilenameExtension = firstNonEmpty(
			overrides.FilenameExtension,
			fileExtension(payload.OriginalObjectKey),
			fileExtension(doc.OriginalObjectKey),
			fileExtension(doc.Name),
		)
	}

	return payload
}

func (f *JobFollower) extractTablesPayloadFromConvert(doc *domain.Document, convertPayload domain.ConvertPayload, correlationID string) domain.ExtractTablesPayload {
	payload := f.extractTablesPayloadForDocument(doc, correlationID)
	payload.OriginalBucket = firstNonEmpty(convertPayload.OriginalBucket, payload.OriginalBucket, doc.OriginalBucket)
	payload.OriginalObjectKey = firstNonEmpty(convertPayload.OriginalObjectKey, payload.OriginalObjectKey, doc.OriginalObjectKey)
	payload.MarkdownBucket = firstNonEmpty(convertPayload.MarkdownBucket, payload.MarkdownBucket, doc.MarkdownBucket)
	payload.MarkdownObjectKey = firstNonEmpty(convertPayload.MarkdownObjectKey, payload.MarkdownObjectKey, resolvedMarkdownKey(doc))
	payload.DoclingMarkdownBucket = firstNonEmpty(convertPayload.DoclingMarkdownBucket, payload.DoclingMarkdownBucket, doc.MarkdownBucket)
	payload.DoclingMarkdownObjectKey = firstNonEmpty(convertPayload.DoclingMarkdownObjectKey, payload.DoclingMarkdownObjectKey, resolvedMarkdownKey(doc))
	payload.DoclingStatus = firstNonEmpty(convertPayload.DoclingStatus, payload.DoclingStatus)
	payload.ConversionStrategy = domain.ConversionStrategy(firstNonEmpty(string(convertPayload.ConversionStrategy), string(payload.ConversionStrategy), string(doc.ConversionStrategy)))
	payload.ExtractTables = convertPayload.ExtractTables || payload.ExtractTables || payload.ConversionStrategy == domain.ConversionStrategyDoclingWithTables || payload.ConversionStrategy == domain.ConversionStrategyTablesOnly
	if !payload.ExtractTables {
		if count, pages := convertPayload.TableInfo(); count > 0 || len(pages) > 0 {
			payload.ExtractTables = true
		}
	}
	return payload
}

func (f *JobFollower) shouldExtractTablesAfterConvert(doc *domain.Document, payload domain.ConvertPayload) bool {
	if f.router == nil || doc == nil {
		return false
	}

	docCopy := *doc
	if payload.ConversionStrategy != "" {
		docCopy.ConversionStrategy = payload.ConversionStrategy
	}

	count, pages := payload.TableInfo()
	return f.router.ShouldExtractTablesAfterConvert(&docCopy, payload.ExtractTables, count, pages)
}

func preprocessConversion(payload map[string]any, doc *domain.Document) (domain.ConvertPayload, string) {
	preprocessPayload := domain.PreprocessPayloadFromMap(payload)
	originalBucket := firstNonEmpty(preprocessPayload.PreprocessedBucket, preprocessPayload.OriginalBucket)
	originalKey := firstNonEmpty(preprocessPayload.PreprocessedObjectKey, preprocessPayload.OriginalObjectKey)
	filenameExtension := firstNonEmpty(
		fileExtension(preprocessPayload.PreprocessedObjectKey),
		fileExtension(preprocessPayload.TargetObjectKey),
	)
	if preprocessPayload.PreprocessedObjectKey != "" || preprocessPayload.TargetObjectKey != "" {
		filenameExtension = "docx"
	}
	if filenameExtension == "" {
		filenameExtension = firstNonEmpty(preprocessPayload.FilenameExtension, "docx")
	}

	correlationID := correlationFromDoc(doc)
	if preprocessPayload.CorrelationID != "" {
		correlationID = preprocessPayload.CorrelationID
	}

	return domain.ConvertPayload{
		OriginalBucket:        originalBucket,
		OriginalObjectKey:     originalKey,
		PreprocessJobID:       preprocessPayload.PreprocessJobID,
		PreprocessedBucket:    preprocessPayload.PreprocessedBucket,
		PreprocessedObjectKey: preprocessPayload.PreprocessedObjectKey,
		TargetObjectKey:       preprocessPayload.TargetObjectKey,
		FilenameExtension:     filenameExtension,
	}, correlationID
}
