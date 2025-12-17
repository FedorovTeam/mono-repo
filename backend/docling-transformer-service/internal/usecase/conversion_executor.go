// backend/docling-transformer-service/internal/usecase/conversion_executor.go
package usecase

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"time"

	"github.com/mono-repo/docling-transformer-service/internal/domain"
	transformations "github.com/mono-repo/docling-transformer-service/internal/domain/transformations"
	"github.com/mono-repo/docling-transformer-service/internal/infrastructure/doclingjson"
	repo "github.com/mono-repo/docling-transformer-service/internal/repository"
	"github.com/mono-repo/docling-transformer-service/internal/usecase/metrics"
	"go.uber.org/zap"
)

type ConversionExecutorConfig struct {
	MarkdownBucket       string
	FastOperationTimeout time.Duration
	HTTPClient           HTTPClient
	UseSourceAsync       bool

	DefaultForceOCR   bool
	DefaultOCRLang    string
	DefaultPDFBackend string

	OverlayMatchRate float64
}

type ConversionExecutor struct {
	repo       repo.QueueRepository
	baseConv   domain.Converter // base: PreferJSON=false
	ocrConv    domain.Converter // ocr phase
	storage    domain.ObjectStorage
	poller     *DoclingPoller
	metrics    *metrics.WorkerMetrics
	logger     *zap.Logger
	httpClient HTTPClient
	cfg        ConversionExecutorConfig
}

func NewConversionExecutor(
	repository repo.QueueRepository,
	baseConv domain.Converter,
	ocrConv domain.Converter,
	storage domain.ObjectStorage,
	poller *DoclingPoller,
	metrics *metrics.WorkerMetrics,
	logger *zap.Logger,
	cfg ConversionExecutorConfig,
) *ConversionExecutor {
	if logger == nil {
		logger = zap.NewNop()
	}

	client := cfg.HTTPClient
	if client == nil {
		timeout := cfg.FastOperationTimeout
		if timeout <= 0 {
			timeout = 30 * time.Second
		}
		client = &http.Client{Timeout: timeout}
	}

	if cfg.OverlayMatchRate <= 0 {
		cfg.OverlayMatchRate = 0.6
	}

	return &ConversionExecutor{
		repo:       repository,
		baseConv:   baseConv,
		ocrConv:    ocrConv,
		storage:    storage,
		poller:     poller,
		metrics:    metrics,
		logger:     logger,
		httpClient: client,
		cfg:        cfg,
	}
}

func (e *ConversionExecutor) ProcessTask(ctx context.Context, task *transformations.TransformationTask) (TaskState, error) {
	if task.MarkdownBucket == "" {
		task.MarkdownBucket = e.cfg.MarkdownBucket
	}
	if task.MarkdownBucket == "" {
		return StateProcessing, fmt.Errorf("markdown bucket is empty")
	}

	if task.MarkdownObjectKey == "" {
		task.MarkdownObjectKey = domain.BuildMarkdownKey(task.OriginalObjectKey)
	}

	if task.DoclingJobID != "" || task.DoclingTaskID != "" {
		return e.pollDocling(ctx, task)
	}

	return e.submitConversion(ctx, task)
}

func (e *ConversionExecutor) pollDocling(ctx context.Context, task *transformations.TransformationTask) (TaskState, error) {
	result, err := e.poller.Poll(ctx, task)
	if err != nil {
		if errors.Is(err, ErrDoclingWaiting) {
			return StateWaitingDocling, nil
		}
		return StateProcessing, err
	}

	mediaType := normalizeMediaType(result.MediaType, result.Charset)
	isOverlay := e.shouldOverlay(task)

	uploadCtx, cancelUpload := e.fastPhaseContext(ctx)
	defer cancelUpload()

	// BASE-фаза overlay: сохраняем base markdown в *.base.md и переводим задачу в OCR-фазу.
	if isOverlay && task.OverlayStage != transformations.OverlayStageOCR {
		tempKey := task.MarkdownObjectKey + ".base.md"

		if err := e.storage.Upload(uploadCtx, task.MarkdownBucket, tempKey, mediaType, result.Content); err != nil {
			return StateProcessing, fmt.Errorf("upload base markdown: %w", err)
		}

		if err := e.repo.PrepareOverlayOCRPhase(ctx, task.ID, task.MarkdownBucket, tempKey); err != nil {
			return StateProcessing, fmt.Errorf("prepare overlay ocr phase: %w", err)
		}

		task.OverlayStage = transformations.OverlayStageOCR
		task.BaseMarkdownBucket = task.MarkdownBucket
		task.BaseMarkdownObjectKey = tempKey

		e.logger.Info("overlay base stored, rescheduled to ocr phase",
			zap.String("queue_id", task.ID.String()),
			zap.String("base_bucket", task.BaseMarkdownBucket),
			zap.String("base_key", task.BaseMarkdownObjectKey),
		)

		return StateRescheduled, nil
	}

	// Обычный режим — просто сохранить и done.
	if !isOverlay {
		if err := e.storage.Upload(uploadCtx, task.MarkdownBucket, task.MarkdownObjectKey, mediaType, result.Content); err != nil {
			return StateProcessing, fmt.Errorf("upload markdown: %w", err)
		}
		if err := e.repo.MarkDone(ctx, task.ID, task.MarkdownBucket, task.MarkdownObjectKey); err != nil {
			return StateProcessing, fmt.Errorf("mark done: %w", err)
		}
		return StateCompleted, nil
	}

	// OCR-фаза overlay: скачать base markdown, наложить таблицы из OCR и сохранить финал.
	if task.BaseMarkdownBucket == "" || task.BaseMarkdownObjectKey == "" {
		return StateProcessing, fmt.Errorf("overlay: base markdown reference is empty")
	}

	baseCtx, cancelBase := e.fastPhaseContext(ctx)
	defer cancelBase()

	baseBytes, err := e.storage.Download(baseCtx, task.BaseMarkdownBucket, task.BaseMarkdownObjectKey)
	if err != nil {
		return StateProcessing, fmt.Errorf("overlay: download base markdown: %w", err)
	}

	baseMD := string(baseBytes)
	ocrMD := string(result.Content)

	mergedMD, stats := doclingjson.OverlayVotingTablesInMarkdown(baseMD, ocrMD, e.cfg.OverlayMatchRate)

	e.logger.Info("overlay tables merged",
		zap.String("queue_id", task.ID.String()),
		zap.Int("merge_total_rows", stats.TotalRows),
		zap.Int("merge_matched_rows", stats.MatchedRows),
		zap.Int("merge_filled_cells", stats.MergedRows),
	)

	if err := e.storage.Upload(uploadCtx, task.MarkdownBucket, task.MarkdownObjectKey, mediaType, []byte(mergedMD)); err != nil {
		return StateProcessing, fmt.Errorf("upload merged markdown: %w", err)
	}

	if err := e.repo.MarkDone(ctx, task.ID, task.MarkdownBucket, task.MarkdownObjectKey); err != nil {
		return StateProcessing, fmt.Errorf("mark done: %w", err)
	}

	return StateCompleted, nil
}

func (e *ConversionExecutor) submitConversion(ctx context.Context, task *transformations.TransformationTask) (TaskState, error) {
	useSourceAsync := e.cfg.UseSourceAsync && task.SourceURL == ""
	if !useSourceAsync && task.SourceURL == "" && task.SourceBucket != "" && task.SourceObjectKey != "" && task.OriginalBucket != "" {
		useSourceAsync = true
	}

	fileExt := task.FilenameExtension
	if fileExt == "" {
		key := task.OriginalObjectKey
		if task.SourceObjectKey != "" {
			key = task.SourceObjectKey
		}
		fileExt = domain.NormalizeExtension(filepath.Ext(key))
	}

	contentType, err := domain.ResolveContentTypeByExtension(fileExt)
	if err != nil {
		return StateProcessing, err
	}

	isOverlay := e.shouldOverlay(task)

	// Выбор конвертера + force_ocr для конкретной фазы
	conv := e.ocrConv
	forceOCR := e.cfg.DefaultForceOCR
	if task.ForceOCR != nil {
		forceOCR = *task.ForceOCR
	}

	if isOverlay && task.OverlayStage != transformations.OverlayStageOCR {
		// base-фаза
		conv = e.baseConv
		forceOCR = false
		task.OverlayStage = transformations.OverlayStageBase
	}

	if isOverlay && task.OverlayStage == transformations.OverlayStageOCR {
		// ocr-фаза
		conv = e.ocrConv
		forceOCR = true
	}

	// только PDF
	if contentType != "application/pdf" {
		forceOCR = false
	}

	ocrLang := e.cfg.DefaultOCRLang
	if task.OCRLanguage != "" {
		ocrLang = task.OCRLanguage
	}

	pdfBackend := e.cfg.DefaultPDFBackend
	if task.PDFBackend != "" {
		pdfBackend = task.PDFBackend
	}

	var sourceContent []byte
	if !useSourceAsync {
		downloadCtx, cancelDownload := e.fastPhaseContext(ctx)
		defer cancelDownload()

		sourceContent, err = e.downloadSource(downloadCtx, task)
		if err != nil {
			return StateProcessing, err
		}
	}

	originalFileName := ResolveFilenameFromURL(task.SourceURL, filepath.Base(task.OriginalObjectKey))
	if filepath.Ext(originalFileName) == "" && fileExt != "" {
		originalFileName = fmt.Sprintf("document.%s", fileExt)
	}

	submitCtx, cancelSubmit := e.fastPhaseContext(ctx)
	defer cancelSubmit()

	sourceBucket := task.OriginalBucket
	sourceObjectKey := task.OriginalObjectKey
	if task.SourceBucket != "" && task.SourceObjectKey != "" {
		sourceBucket = task.SourceBucket
		sourceObjectKey = task.SourceObjectKey
	}

	if useSourceAsync && (sourceBucket == "" || sourceObjectKey == "") {
		return StateProcessing, fmt.Errorf("source bucket/object key are required for source_async mode")
	}

	// overlay: base OCR off, ocr-phase OCR on
	enableOCR := (*bool)(nil)
	if isOverlay && task.OverlayStage != transformations.OverlayStageOCR {
		enableOCR = boolPtr(false)
	}
	if isOverlay && task.OverlayStage == transformations.OverlayStageOCR {
		enableOCR = boolPtr(true)
	}

	convertReq := domain.ConvertRequest{
		FileName:        originalFileName,
		Content:         sourceContent,
		ContentType:     contentType,
		SourceBucket:    sourceBucket,
		SourceObjectKey: sourceObjectKey,
		TargetBucket:    task.MarkdownBucket,
		TargetObjectKey: task.MarkdownObjectKey,
		EnableOCR:       enableOCR,
		ForceOCR:        boolPtr(forceOCR),
		OCRLanguage:     ocrLang,
		PDFBackend:      pdfBackend,
	}

	ids, err := conv.Submit(submitCtx, convertReq)
	if err != nil {
		if state, handleErr, handled := e.handleTemporarySubmitError(ctx, task, err); handled {
			return state, handleErr
		}
		return StateProcessing, fmt.Errorf("docling submit: %w", err)
	}

	e.metrics.RecordDoclingSubmit(ctx, ids, forceOCR, ocrLang, pdfBackend)

	nextPoll := e.poller.NextPollTime(time.Now(), 0)
	if err := e.repo.MarkDoclingWaiting(ctx, task.ID, ids.TaskID, ids.JobID, 0, nextPoll); err != nil {
		return StateProcessing, fmt.Errorf("store docling job: %w", err)
	}

	task.DoclingTaskID = ids.TaskID
	task.DoclingJobID = ids.JobID
	task.DoclingPollCount = 0
	task.NextPollAt = &nextPoll

	e.logger.Info("docling job submitted",
		zap.String("queue_id", task.ID.String()),
		zap.String("docling_task_id", ids.TaskID),
		zap.String("overlay_stage", string(task.OverlayStage)),
		zap.Bool("force_ocr", forceOCR),
	)

	return StateWaitingDocling, nil
}

func (e *ConversionExecutor) shouldOverlay(task *transformations.TransformationTask) bool {
	if task == nil || task.ForceOCR == nil || !*task.ForceOCR {
		return false
	}
	ext := domain.NormalizeExtension(task.FilenameExtension)
	if ext == "" {
		ext = domain.NormalizeExtension(filepath.Ext(task.OriginalObjectKey))
	}
	return ext == "pdf"
}

func (e *ConversionExecutor) downloadSource(ctx context.Context, task *transformations.TransformationTask) ([]byte, error) {
	if task.SourceURL != "" {
		return e.downloadFromURL(ctx, task.SourceURL)
	}
	if task.SourceBucket != "" && task.SourceObjectKey != "" {
		content, err := e.storage.Download(ctx, task.SourceBucket, task.SourceObjectKey)
		if err != nil {
			return nil, fmt.Errorf("download source object: %w", err)
		}
		return content, nil
	}

	content, err := e.storage.Download(ctx, task.OriginalBucket, task.OriginalObjectKey)
	if err != nil {
		return nil, fmt.Errorf("download original: %w", err)
	}
	return content, nil
}

func (e *ConversionExecutor) downloadFromURL(ctx context.Context, rawURL string) ([]byte, error) {
	if e.httpClient == nil {
		return nil, fmt.Errorf("download source: http client is not configured")
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, fmt.Errorf("download source: build request: %w", err)
	}

	resp, err := e.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("download source: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return nil, fmt.Errorf("download source: status %d: %s", resp.StatusCode, string(body))
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("download source: read body: %w", err)
	}
	if len(data) == 0 {
		return nil, fmt.Errorf("download source: empty body")
	}

	return data, nil
}

func (e *ConversionExecutor) fastPhaseContext(ctx context.Context) (context.Context, context.CancelFunc) {
	if e.cfg.FastOperationTimeout <= 0 {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, e.cfg.FastOperationTimeout)
}

func (e *ConversionExecutor) handleTemporarySubmitError(ctx context.Context, task *transformations.TransformationTask, err error) (TaskState, error, bool) {
	var tmpErr *domain.TemporarySubmitRetryExhaustedError
	if !errors.As(err, &tmpErr) {
		return StateProcessing, nil, false
	}

	if e.poller == nil {
		return StateProcessing, fmt.Errorf("docling submit: %w", err), true
	}

	retryIndex := task.RetryCount
	if retryIndex < 0 {
		retryIndex = 0
	}

	nextPoll := e.poller.NextPollTime(time.Now(), retryIndex)
	if markErr := e.repo.MarkDoclingWaiting(ctx, task.ID, task.DoclingTaskID, task.DoclingJobID, task.DoclingPollCount, nextPoll); markErr != nil {
		return StateProcessing, fmt.Errorf("store docling waiting: %w", markErr), true
	}

	task.NextPollAt = &nextPoll

	e.logger.Warn("docling submit temporary error, rescheduled",
		zap.String("queue_id", task.ID.String()),
		zap.Time("next_poll_at", nextPoll),
		zap.Error(err),
	)

	return StateWaitingDocling, nil, true
}

func boolPtr(v bool) *bool { return &v }
