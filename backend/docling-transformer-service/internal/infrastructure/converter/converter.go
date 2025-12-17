package converter

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"mime"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"path/filepath"
	"strings"
	"time"

	"github.com/mono-repo/docling-transformer-service/internal/domain"
	transformations "github.com/mono-repo/docling-transformer-service/internal/domain/transformations"
	"github.com/mono-repo/docling-transformer-service/internal/infrastructure/doclingjson"
	"github.com/mono-repo/docling-transformer-service/internal/infrastructure/telemetry"
	"go.uber.org/zap"
)

const (
	// Async конвертация файла
	doclingSubmitPath       = "/v1/convert/file/async"
	doclingSourceSubmitPath = "/v1/convert/source/async"
	// Поллинг статуса задачи
	doclingStatusPath = "/v1/status/poll/"
	// Получение результата задачи
	doclingResultPath = "/v1/result/"

	correlationIDHeader = "X-Correlation-Id"
	sourceFileHeader    = "X-Docling-Source-Filename"
	endpointHeader      = "X-Docling-Endpoint"
	apiKeyHeader        = "X-Api-Key"

	doclingFileFieldName = "files"

	defaultExtension       = "docx"
	submitRetryAttempts    = 5
	submitRetryInitialWait = 500 * time.Millisecond
	submitRetryBackoff     = 2.0
	submitRetryMaxWait     = 5 * time.Second

	doclingModeUploadFile  = "upload_file"
	doclingModeSourceAsync = "source_async"

	maxResponseSnippetLen = 1024
)

var successStatuses = map[string]struct{}{
	"success":         {},
	"partial_success": {},
	"succeeded":       {},
	"completed":       {},
	"done":            {},
	"finished":        {},
}

var pendingStatuses = map[string]struct{}{
	"pending":     {},
	"started":     {},
	"processing":  {},
	"queued":      {},
	"running":     {},
	"in_progress": {},
	"accepted":    {},
	"waiting":     {},
	"created":     {},
	"in-progress": {},
	"in progress": {},
}

func isSuccessStatus(status string) bool {
	_, ok := successStatuses[status]
	return ok
}

func isPendingStatus(status string) bool {
	_, ok := pendingStatuses[status]
	return ok
}

var ErrDoclingPollingAttemptsExceeded = transformations.ErrPollingAttemptsExceeded

type temporaryDoclingError struct {
	statusCode int
	body       string
	cause      error
}

func (e *temporaryDoclingError) Error() string   { return e.cause.Error() }
func (e *temporaryDoclingError) Unwrap() error   { return e.cause }
func (e *temporaryDoclingError) Temporary() bool { return true }
func (e *temporaryDoclingError) StatusCode() int { return e.statusCode }
func (e *temporaryDoclingError) Body() string    { return e.body }

func isTemporaryDoclingStatus(statusCode int) bool {
	if statusCode >= 500 && statusCode < 600 {
		return true
	}

	switch statusCode {
	case http.StatusNotFound, http.StatusTooEarly, http.StatusTooManyRequests:
		return true
	}

	return false
}

func responseSnippet(body []byte) string {
	if len(body) == 0 {
		return ""
	}

	snippet := strings.TrimSpace(string(body))
	if len(snippet) > maxResponseSnippetLen {
		return snippet[:maxResponseSnippetLen] + "..."
	}

	return snippet
}

// Config описывает параметры HTTP-клиента Docling.
type Config struct {
	Endpoint           string
	APIKey             string
	DefaultLanguage    string
	PDFDefaultLanguage string
	PDFEnableOCR       bool
	ForceOCR           bool
	PDFBackend         string
	Mode               string
	Storage            StorageConfig
	TraceHttpRequest   bool
}

// StorageConfig описывает настройки доступа Docling к MinIO/S3.
type StorageConfig struct {
	Endpoint  string
	AccessKey string
	SecretKey string
	Bucket    string
	Region    string
	UseSSL    bool
}

func (c *DoclingConverter) resolveSourceStorage(req domain.ConvertRequest) (storageSettings, error) {
	if req.SourceBucket == "" || req.SourceObjectKey == "" {
		return storageSettings{}, errors.New("source bucket/object key is required for source_async mode")
	}

	return c.storageFromConfig()
}

func (c *DoclingConverter) resolveTargetStorage(req domain.ConvertRequest) (storageSettings, error) {
	if req.TargetBucket == "" && req.TargetObjectKey == "" {
		return storageSettings{}, nil
	}

	return c.storageFromConfig()
}

func (c *DoclingConverter) storageFromConfig() (storageSettings, error) {
	if c.storage.Endpoint == "" || c.storage.AccessKey == "" || c.storage.SecretKey == "" {
		return storageSettings{}, errors.New("docling storage config is incomplete")
	}

	return storageSettings{
		Endpoint:  c.storage.Endpoint,
		AccessKey: c.storage.AccessKey,
		SecretKey: c.storage.SecretKey,
		Region:    c.storage.Region,
		UseSSL:    c.storage.UseSSL,
	}, nil
}

func normalizeStatus(status string) string {
	return strings.ToLower(strings.TrimSpace(status))
}

// ----- Структуры под реальные ответы Docling Serve -----

// TaskStatusResponse из /v1/status/poll/{task_id}
type taskStatusResponse struct {
	TaskID     string              `json:"task_id"`
	TaskType   string              `json:"task_type"`
	TaskStatus string              `json:"task_status"`
	TaskPos    *int                `json:"task_position"`
	TaskMeta   *taskProcessingMeta `json:"task_meta"`
}

type taskProcessingMeta struct {
	NumDocs      int `json:"num_docs"`
	NumProcessed int `json:"num_processed"`
	NumSucceeded int `json:"num_succeeded"`
	NumFailed    int `json:"num_failed"`
}

// ConvertDocumentResponse из /v1/result/{task_id}
type convertDocumentResponse struct {
	Document       exportDocumentResponse `json:"document"`
	Status         string                 `json:"status"`
	Errors         []errorItem            `json:"errors"`
	ProcessingTime float64                `json:"processing_time"`
	Timings        map[string]any         `json:"timings"`
}

type exportDocumentResponse struct {
	Filename       string  `json:"filename"`
	MdContent      *string `json:"md_content"`
	JsonContent    any     `json:"json_content"`
	HtmlContent    *string `json:"html_content"`
	TextContent    *string `json:"text_content"`
	DoctagsContent *string `json:"doctags_content"`
	Charset        *string `json:"charset"`
}

type errorItem struct {
	ComponentType string `json:"component_type"`
	ModuleName    string `json:"module_name"`
	ErrorMessage  string `json:"error_message"`
}

type doclingSubmitResponse struct {
	JobID  string `json:"job_id"`
	TaskID string `json:"task_id"`
}

type convertDocumentsRequest struct {
	Sources []s3SourceRequest `json:"sources"`
	Target  *s3TargetRequest  `json:"target,omitempty"`
	Options doclingOptions    `json:"options,omitempty"`
}

type s3SourceRequest struct {
	Kind      string `json:"kind"`
	Endpoint  string `json:"endpoint"`
	VerifySSL bool   `json:"verify_ssl"`
	AccessKey string `json:"access_key"`
	SecretKey string `json:"secret_key"`
	Bucket    string `json:"bucket"`
	KeyPrefix string `json:"key_prefix"`
	Region    string `json:"region,omitempty"`
}

type s3TargetRequest struct {
	Kind      string `json:"kind"`
	Endpoint  string `json:"endpoint"`
	VerifySSL bool   `json:"verify_ssl"`
	AccessKey string `json:"access_key"`
	SecretKey string `json:"secret_key"`
	Bucket    string `json:"bucket"`
	KeyPrefix string `json:"key_prefix"`
	Region    string `json:"region,omitempty"`
}

type storageSettings struct {
	Endpoint  string
	AccessKey string
	SecretKey string
	Region    string
	UseSSL    bool
}

type doclingOptions struct {
	ToFormats         []string `json:"to_formats,omitempty"`
	FromFormats       []string `json:"from_formats,omitempty"`
	Language          string   `json:"language,omitempty"`
	OCR               *bool    `json:"ocr,omitempty"`
	DoOCR             *bool    `json:"do_ocr,omitempty"`
	ForceOCR          *bool    `json:"force_ocr,omitempty"`
	OCREngine         string   `json:"ocr_engine,omitempty"`
	OCRLang           string   `json:"ocr_lang,omitempty"`
	PDFBackend        string   `json:"pdf_backend,omitempty"`
	TableMode         string   `json:"table_mode,omitempty"`
	TableCellMatching *bool    `json:"table_cell_matching,omitempty"`
	ImageExportMode   string   `json:"image_export_mode,omitempty"`
	Pipeline          string   `json:"pipeline,omitempty"`
}

// DoclingConverter вызывает HTTP-обёртку Docling, прокидывая correlation-id.
type DoclingConverter struct {
	endpoint           string
	apiKey             string
	defaultLanguage    string
	pdfDefaultLanguage string
	pdfEnableOCR       bool
	forceOCR           bool
	pdfBackend         string
	mode               string
	storage            StorageConfig
	client             *http.Client
	logger             *zap.Logger
}

// NewDoclingConverter создаёт конвертер с параметрами по умолчанию.
func NewDoclingConverter(cfg Config, logger *zap.Logger) *DoclingConverter {
	if logger == nil {
		logger = zap.NewNop()
	}

	mode := strings.TrimSpace(cfg.Mode)
	if mode == "" {
		mode = doclingModeUploadFile
	}

	storage := cfg.Storage
	if storage.Bucket == "" {
		storage.Bucket = cfg.Storage.Bucket
	}

	pdfBackend := strings.TrimSpace(cfg.PDFBackend)
	if pdfBackend == "" {
		pdfBackend = "pypdfium2"
	}
	httpClient := &http.Client{
		Timeout: 60 * time.Second,
	}
	if cfg.TraceHttpRequest {
		httpClient.Transport = newLoggingTransport(httpClient.Transport, logger)
	}

	return &DoclingConverter{
		endpoint:           strings.TrimRight(cfg.Endpoint, "/"),
		apiKey:             cfg.APIKey,
		defaultLanguage:    cfg.DefaultLanguage,
		pdfDefaultLanguage: cfg.PDFDefaultLanguage,
		pdfEnableOCR:       cfg.PDFEnableOCR,
		forceOCR:           cfg.ForceOCR,
		pdfBackend:         pdfBackend,
		mode:               mode,
		storage:            storage,
		client:             httpClient,
		logger:             logger,
	}
}

func resolveContentTypeAndExt(reqContentType, fileName string) (string, string, error) {
	normalizedContentType := strings.ToLower(reqContentType)
	if ext, ok := domain.ResolveExtensionByContentType(normalizedContentType); ok {
		return normalizedContentType, ext, nil
	}

	fileExt := strings.TrimPrefix(strings.ToLower(filepath.Ext(fileName)), ".")
	if fileExt != "" {
		contentType, err := domain.ResolveContentTypeByExtension(fileExt)
		if err != nil {
			return "", "", err
		}
		return contentType, fileExt, nil
	}

	if normalizedContentType != "" {
		return "", "", fmt.Errorf("unsupported content type: %s", reqContentType)
	}

	contentType, err := domain.ResolveContentTypeByExtension("")
	if err != nil {
		return "", "", err
	}

	return contentType, "docx", nil
}

func (c *DoclingConverter) shouldEnableOCR(contentType string) bool {
	return contentType == "application/pdf" && c.pdfEnableOCR
}

func (c *DoclingConverter) resolveOCROptions(contentType string, req domain.ConvertRequest) (enableOCR, forceOCR bool, ocrLang, pdfBackend string) {
	enableOCR = c.shouldEnableOCR(contentType)
	forceOCR = c.forceOCR
	if req.EnableOCR != nil {
		enableOCR = *req.EnableOCR
	}
	if req.ForceOCR != nil {
		forceOCR = *req.ForceOCR
	}

	if forceOCR {
		enableOCR = true
	}

	ocrLang = c.selectLanguage(contentType)
	if req.OCRLanguage != "" {
		ocrLang = req.OCRLanguage
	}

	pdfBackend = c.pdfBackend
	if req.PDFBackend != "" {
		pdfBackend = req.PDFBackend
	}

	if contentType != "application/pdf" {
		enableOCR = false
		forceOCR = false
	}

	return enableOCR, forceOCR, ocrLang, pdfBackend
}

func (c *DoclingConverter) selectLanguage(contentType string) string {
	if contentType == "application/pdf" && c.pdfDefaultLanguage != "" {
		return c.pdfDefaultLanguage
	}
	return c.defaultLanguage
}

// Submit отправляет документ в Docling и возвращает идентификаторы задачи.
func (c *DoclingConverter) Submit(ctx context.Context, req domain.ConvertRequest) (domain.DoclingIdentifiers, error) {
	ctx, correlationID := telemetry.EnsureCorrelationID(ctx)

	contentType, fileExt, err := resolveContentTypeAndExt(req.ContentType, req.FileName)
	if err != nil {
		return domain.DoclingIdentifiers{}, err
	}

	filename := req.FileName
	if filename == "" {
		filename = fmt.Sprintf("document.%s", fileExt)
	}

	enableOCR, forceOCR, ocrLang, pdfBackend := c.resolveOCROptions(contentType, req)

	switch c.mode {
	case doclingModeSourceAsync:
		ids, err := c.submitSourceAsyncWithRetry(ctx, correlationID, filename, contentType, fileExt, req)
		if err != nil {
			return domain.DoclingIdentifiers{}, err
		}

		logFields := []zap.Field{
			zap.String("docling_task_id", ids.TaskID),
			zap.String("primary_docling_id", ids.TaskID),
			zap.String("correlation_id", correlationID),
			zap.String("content_type", contentType),
			zap.String("file_ext", fileExt),
		}
		if ids.JobID != "" {
			logFields = append(logFields, zap.String("docling_job_id", ids.JobID))
		}

		c.logger.Info("docling submit accepted", logFields...)

		return ids, nil
	default:
		if len(req.Content) == 0 {
			return domain.DoclingIdentifiers{}, errors.New("empty document")
		}

		ids, err := c.submitWithRetry(ctx, correlationID, filename, contentType, fileExt, req.Content, enableOCR, forceOCR, ocrLang, pdfBackend)
		if err != nil {
			return domain.DoclingIdentifiers{}, err
		}

		logFields := []zap.Field{
			zap.String("docling_task_id", ids.TaskID),
			zap.String("primary_docling_id", ids.TaskID),
			zap.String("correlation_id", correlationID),
			zap.String("content_type", contentType),
			zap.String("file_ext", fileExt),
		}
		if ids.JobID != "" {
			logFields = append(logFields, zap.String("docling_job_id", ids.JobID))
		}

		c.logger.Info("docling submit accepted", logFields...)

		return ids, nil
	}
}

// CheckStatus делает одиночный запрос статуса Docling и возвращает результат при готовности.
func (c *DoclingConverter) CheckStatus(ctx context.Context, taskID string) (domain.StatusCheckResult, error) {
	if taskID == "" {
		return domain.StatusCheckResult{}, errors.New("empty docling task id")
	}

	ctx, correlationID := telemetry.EnsureCorrelationID(ctx)

	logFields := []zap.Field{
		zap.String("docling_task_id", taskID),
		zap.String("primary_docling_id", taskID),
		zap.String("correlation_id", correlationID),
	}

	statusResp, body, err := c.fetchStatus(ctx, correlationID, taskID)
	if err != nil {
		return domain.StatusCheckResult{}, err
	}

	rawStatus := strings.TrimSpace(statusResp.TaskStatus)
	normalizedStatus := normalizeStatus(rawStatus)

	if normalizedStatus == "" {
		normalizedStatus = "pending"
		c.logger.Warn("docling status missing, defaulting to pending", logFields...)
	}

	switch {
	case isSuccessStatus(normalizedStatus):
		resultResp, resultBody, err := c.fetchResult(ctx, correlationID, taskID)
		if err != nil {
			return domain.StatusCheckResult{}, fmt.Errorf("docling result fetch failed: %w (body=%s)", err, responseSnippet(resultBody))
		}

		result, buildErr := buildConvertResult(resultResp)
		if buildErr != nil {
			return domain.StatusCheckResult{}, fmt.Errorf("docling result: %w", buildErr)
		}

		c.logger.Info("docling conversion completed", logFields...)

		return domain.StatusCheckResult{
			Status: normalizedStatus,
			Result: &result,
		}, nil

	case isPendingStatus(normalizedStatus):
		c.logger.Debug("docling still processing", append(logFields, zap.String("status", normalizedStatus))...)
		return domain.StatusCheckResult{
			Status: normalizedStatus,
			Result: nil,
		}, nil

	default:
		c.logger.Warn("docling reported failure status", append(logFields,
			zap.String("status", rawStatus),
			zap.String("status_body", responseSnippet(body)),
		)...)

		if resultResp, resultBody, resErr := c.fetchResult(ctx, correlationID, taskID); resErr == nil {
			if len(resultResp.Errors) > 0 {
				for _, e := range resultResp.Errors {
					c.logger.Error("docling conversion error detail", append(logFields,
						zap.String("component_type", e.ComponentType),
						zap.String("module_name", e.ModuleName),
						zap.String("error_message", e.ErrorMessage),
					)...)
				}
			} else {
				c.logger.Warn("docling result on failure has no errors", append(logFields,
					zap.String("result_body", responseSnippet(resultBody)),
				)...)
			}
		} else {
			c.logger.Warn("docling result fetch on failure errored", append(logFields, zap.Error(resErr))...)
		}

		return domain.StatusCheckResult{}, fmt.Errorf("docling status=%s for task %s: response=%s", rawStatus, taskID, responseSnippet(body))
	}
}

// --- внутренняя отправка задач и опции Docling ---

func (c *DoclingConverter) submitWithRetry(
	ctx context.Context,
	correlationID, filename, contentType, fileExt string,
	content []byte,
	enableOCR, forceOCR bool,
	ocrLang, pdfBackend string,
) (domain.DoclingIdentifiers, error) {
	var lastErr error

	for attempt := 1; attempt <= submitRetryAttempts; attempt++ {
		ids, err := c.submit(ctx, correlationID, filename, contentType, fileExt, content, enableOCR, forceOCR, ocrLang, pdfBackend)
		if err == nil {
			return ids, nil
		}

		lastErr = err
		isTemporary, statusCode := isTemporaryDoclingError(err)
		delay := submitRetryDelay(attempt)

		fields := []zap.Field{
			zap.String("correlation_id", correlationID),
			zap.Int("attempt", attempt),
			zap.Bool("temporary", isTemporary),
			zap.Int("status_code", statusCode),
			zap.Error(err),
		}
		if attempt < submitRetryAttempts {
			fields = append(fields, zap.Duration("retry_in", delay))
			c.logger.Warn("docling submit failed", fields...)

			select {
			case <-ctx.Done():
				return domain.DoclingIdentifiers{}, ctx.Err()
			case <-time.After(delay):
			}
		} else {
			c.logger.Error("docling submit failed", fields...)
		}
	}

	if isTemporary, _ := isTemporaryDoclingError(lastErr); isTemporary {
		return domain.DoclingIdentifiers{}, &domain.TemporarySubmitRetryExhaustedError{Err: lastErr}
	}

	return domain.DoclingIdentifiers{}, lastErr
}

func (c *DoclingConverter) submitSourceAsyncWithRetry(
	ctx context.Context,
	correlationID, filename, contentType, fileExt string,
	req domain.ConvertRequest,
) (domain.DoclingIdentifiers, error) {
	var lastErr error

	for attempt := 1; attempt <= submitRetryAttempts; attempt++ {
		ids, err := c.submitSourceAsync(ctx, correlationID, filename, contentType, fileExt, req)
		if err == nil {
			return ids, nil
		}

		lastErr = err
		isTemporary, statusCode := isTemporaryDoclingError(err)
		delay := submitRetryDelay(attempt)

		fields := []zap.Field{
			zap.String("correlation_id", correlationID),
			zap.Int("attempt", attempt),
			zap.Bool("temporary", isTemporary),
			zap.Int("status_code", statusCode),
			zap.Error(err),
		}
		if attempt < submitRetryAttempts {
			fields = append(fields, zap.Duration("retry_in", delay))
			c.logger.Warn("docling submit failed", fields...)

			select {
			case <-ctx.Done():
				return domain.DoclingIdentifiers{}, ctx.Err()
			case <-time.After(delay):
			}
		} else {
			c.logger.Error("docling submit failed", fields...)
		}
	}

	if isTemporary, _ := isTemporaryDoclingError(lastErr); isTemporary {
		return domain.DoclingIdentifiers{}, &domain.TemporarySubmitRetryExhaustedError{Err: lastErr}
	}

	return domain.DoclingIdentifiers{}, lastErr
}

// writeDoclingOptions — формирует form-data поля с опциями Docling Serve.
func (c *DoclingConverter) writeDoclingOptions(
	writer *multipart.Writer,
	contentType, fileExt string,
	enableOCR, forceOCR bool,
	ocrLang, pdfBackend string,
) error {
	// Всегда явно просим JSON; Markdown оставляем как запасной формат
	if err := writer.WriteField("to_formats", "json,md"); err != nil {
		return fmt.Errorf("write to_formats: %w", err)
	}

	// Подсказываем тип входа (pdf/doc/docx) — опционально
	if fileExt != "" {
		if err := writer.WriteField("from_formats", fileExt); err != nil {
			return fmt.Errorf("write from_formats: %w", err)
		}
	}

	if lang := c.selectLanguage(contentType); lang != "" {
		if err := writer.WriteField("language", lang); err != nil {
			return fmt.Errorf("write language: %w", err)
		}
	}

	// Опции для PDF + OCR
	if enableOCR {
		if err := writer.WriteField("ocr", "true"); err != nil {
			return fmt.Errorf("write ocr: %w", err)
		}

		// Включаем OCR (Docling сам решит, когда его применять)
		if err := writer.WriteField("do_ocr", "true"); err != nil {
			return fmt.Errorf("write do_ocr: %w", err)
		}

		if forceOCR {
			if err := writer.WriteField("force_ocr", "true"); err != nil {
				return fmt.Errorf("write force_ocr: %w", err)
			}
		}

		// Дадим Docling самому выбрать доступный движок
		if err := writer.WriteField("ocr_engine", "tesseract"); err != nil {
			return fmt.Errorf("write ocr_engine: %w", err)
		}

		// Язык OCR
		if ocrLang != "" {
			if err := writer.WriteField("ocr_lang", normalizeTesseractLang(ocrLang)); err != nil {
				return fmt.Errorf("write ocr_lang: %w", err)
			}
		}

		// Бэкенд парсера PDF
		backend := pdfBackend
		if backend == "" {
			backend = "pypdfium2"
		}
		if err := writer.WriteField("pdf_backend", backend); err != nil {
			return fmt.Errorf("write pdf_backend: %w", err)
		}
	}

	// Режим таблиц и картинок — дефолты, но укажем явно
	if err := writer.WriteField("table_mode", "accurate"); err != nil {
		return fmt.Errorf("write table_mode: %w", err)
	}
	if err := writer.WriteField("table_cell_matching", "true"); err != nil {
		return fmt.Errorf("write table_cell_matching: %w", err)
	}
	if err := writer.WriteField("image_export_mode", "embedded"); err != nil {
		return fmt.Errorf("write image_export_mode: %w", err)
	}
	// Стандартный pipeline
	if err := writer.WriteField("pipeline", "standard"); err != nil {
		return fmt.Errorf("write pipeline: %w", err)
	}

	return nil
}

func (c *DoclingConverter) buildDoclingOptions(contentType, fileExt string, enableOCR, forceOCR bool, ocrLang, pdfBackend string) doclingOptions {
	opts := doclingOptions{
		ToFormats:         []string{"json", "md"},
		TableMode:         "accurate",
		TableCellMatching: boolPtr(true),
		ImageExportMode:   "embedded",
		Pipeline:          "standard",
	}

	if fileExt != "" {
		opts.FromFormats = []string{fileExt}
	}

	if lang := c.selectLanguage(contentType); lang != "" {
		opts.Language = lang
		if ocrLang == "" {
			ocrLang = lang
		}
	}

	if enableOCR {
		opts.OCR = boolPtr(true)
		opts.DoOCR = boolPtr(true)
		if forceOCR {
			opts.ForceOCR = boolPtr(true)
		}
		if ocrLang != "" {
			opts.OCRLang = ocrLang
		}
		if pdfBackend != "" {
			opts.PDFBackend = pdfBackend
		} else {
			opts.PDFBackend = "pypdfium2"
		}
		opts.OCREngine = "tesseract"
		if ocrLang != "" {
			opts.OCRLang = normalizeTesseractLang(ocrLang)
		}
	}

	return opts
}

func (c *DoclingConverter) submit(
	ctx context.Context,
	correlationID, filename, contentType, fileExt string,
	content []byte,
	enableOCR, forceOCR bool,
	ocrLang, pdfBackend string,
) (domain.DoclingIdentifiers, error) {
	endpoint := fmt.Sprintf("%s%s", c.endpoint, doclingSubmitPath)

	submitURL, err := url.Parse(endpoint)
	if err != nil {
		return domain.DoclingIdentifiers{}, fmt.Errorf("parse docling submit endpoint: %w", err)
	}

	query := submitURL.Query()
	if lang := c.selectLanguage(contentType); lang != "" {
		query.Set("language", lang)
	}
	if enableOCR {
		query.Set("ocr", "true")
	}
	submitURL.RawQuery = query.Encode()

	payload := &bytes.Buffer{}
	writer := multipart.NewWriter(payload)

	contentDisposition := mime.FormatMediaType("form-data", map[string]string{
		"name":     doclingFileFieldName,
		"filename": filename,
	})
	if contentDisposition == "" {
		return domain.DoclingIdentifiers{}, fmt.Errorf("build docling multipart: could not format content disposition")
	}

	partHeaders := textproto.MIMEHeader{}
	partHeaders.Set("Content-Disposition", contentDisposition)
	partHeaders.Set("Content-Type", contentType)

	part, err := writer.CreatePart(partHeaders)
	if err == nil {
		_, err = part.Write(content)
	}
	if err != nil {
		return domain.DoclingIdentifiers{}, fmt.Errorf("build docling multipart: %w", err)
	}

	// Опции Docling Serve (форматы, OCR, язык, pdf_backend и т.п.)
	if err = c.writeDoclingOptions(writer, contentType, fileExt, enableOCR, forceOCR, ocrLang, pdfBackend); err != nil {
		return domain.DoclingIdentifiers{}, err
	}

	if err = writer.Close(); err != nil {
		return domain.DoclingIdentifiers{}, fmt.Errorf("build docling multipart: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, submitURL.String(), payload)
	if err != nil {
		return domain.DoclingIdentifiers{}, fmt.Errorf("build docling request: %w", err)
	}

	httpReq.Header.Set("Content-Type", writer.FormDataContentType())
	httpReq.Header.Set(correlationIDHeader, correlationID)
	if c.apiKey != "" {
		httpReq.Header.Set(apiKeyHeader, c.apiKey)
	}
	if filename != "" {
		httpReq.Header.Set(sourceFileHeader, filename)
	}
	httpReq.Header.Set(endpointHeader, c.endpoint)

	resp, err := c.client.Do(httpReq)
	if err != nil {
		return domain.DoclingIdentifiers{}, fmt.Errorf("call docling submit: %w", err)
	}
	defer resp.Body.Close()

	body, readErr := io.ReadAll(resp.Body)
	if readErr != nil {
		return domain.DoclingIdentifiers{}, fmt.Errorf("read docling submit response: %w", readErr)
	}

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusAccepted {
		baseErr := fmt.Errorf("docling submit returned status %d: %s", resp.StatusCode, string(body))
		if resp.StatusCode >= 500 && resp.StatusCode < 600 {
			return domain.DoclingIdentifiers{}, &temporaryDoclingError{statusCode: resp.StatusCode, body: responseSnippet(body), cause: baseErr}
		}

		return domain.DoclingIdentifiers{}, baseErr
	}

	var submitResp doclingSubmitResponse
	if err := json.Unmarshal(body, &submitResp); err != nil {
		return domain.DoclingIdentifiers{}, fmt.Errorf("unmarshal docling submit response: %w", err)
	}

	ids := domain.DoclingIdentifiers{TaskID: submitResp.TaskID, JobID: submitResp.JobID}
	if ids.TaskID == "" {
		return domain.DoclingIdentifiers{}, errors.New("docling submit response has empty task identifier")
	}

	return ids, nil
}

func (c *DoclingConverter) submitSourceAsync(
	ctx context.Context,
	correlationID, filename, contentType, fileExt string,
	req domain.ConvertRequest,
) (domain.DoclingIdentifiers, error) {
	endpoint := fmt.Sprintf("%s%s", c.endpoint, doclingSourceSubmitPath)
	sourceStorage, err := c.resolveSourceStorage(req)
	if err != nil {
		return domain.DoclingIdentifiers{}, err
	}

	targetStorage, err := c.resolveTargetStorage(req)
	if err != nil {
		return domain.DoclingIdentifiers{}, err
	}

	enableOCR, forceOCR, ocrLang, pdfBackend := c.resolveOCROptions(contentType, req)

	requestBody := convertDocumentsRequest{
		Sources: []s3SourceRequest{
			{
				Kind:      "s3",
				Endpoint:  sourceStorage.Endpoint,
				VerifySSL: sourceStorage.UseSSL,
				AccessKey: sourceStorage.AccessKey,
				SecretKey: sourceStorage.SecretKey,
				Bucket:    req.SourceBucket,
				KeyPrefix: strings.TrimPrefix(req.SourceObjectKey, "/"),
				Region:    sourceStorage.Region,
			},
		},
		Options: c.buildDoclingOptions(contentType, fileExt, enableOCR, forceOCR, ocrLang, pdfBackend),
	}

	if req.TargetBucket != "" {
		requestBody.Target = &s3TargetRequest{
			Kind:      "s3",
			Endpoint:  targetStorage.Endpoint,
			VerifySSL: targetStorage.UseSSL,
			AccessKey: targetStorage.AccessKey,
			SecretKey: targetStorage.SecretKey,
			Bucket:    req.TargetBucket,
			KeyPrefix: strings.TrimPrefix(req.TargetObjectKey, "/"),
			Region:    targetStorage.Region,
		}
	}

	payload, err := json.Marshal(requestBody)
	if err != nil {
		return domain.DoclingIdentifiers{}, fmt.Errorf("marshal docling source request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return domain.DoclingIdentifiers{}, fmt.Errorf("build docling source request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set(correlationIDHeader, correlationID)
	httpReq.Header.Set(endpointHeader, c.endpoint)
	if c.apiKey != "" {
		httpReq.Header.Set(apiKeyHeader, c.apiKey)
	}
	if filename != "" {
		httpReq.Header.Set(sourceFileHeader, filename)
	}

	resp, err := c.client.Do(httpReq)
	if err != nil {
		return domain.DoclingIdentifiers{}, fmt.Errorf("call docling source submit: %w", err)
	}
	defer resp.Body.Close()

	body, readErr := io.ReadAll(resp.Body)
	if readErr != nil {
		return domain.DoclingIdentifiers{}, fmt.Errorf("read docling source submit response: %w", readErr)
	}

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusAccepted {
		baseErr := fmt.Errorf("docling submit returned status %d: %s", resp.StatusCode, string(body))
		if resp.StatusCode >= 500 && resp.StatusCode < 600 {
			return domain.DoclingIdentifiers{}, &temporaryDoclingError{statusCode: resp.StatusCode, body: responseSnippet(body), cause: baseErr}
		}

		return domain.DoclingIdentifiers{}, baseErr
	}

	var submitResp doclingSubmitResponse
	if err := json.Unmarshal(body, &submitResp); err != nil {
		return domain.DoclingIdentifiers{}, fmt.Errorf("unmarshal docling submit response: %w", err)
	}

	ids := domain.DoclingIdentifiers{TaskID: submitResp.TaskID, JobID: submitResp.JobID}
	if ids.TaskID == "" {
		return domain.DoclingIdentifiers{}, errors.New("docling submit response has empty task identifier")
	}

	return ids, nil
}

// fetchStatus читает TaskStatusResponse из /v1/status/poll/{task_id}
func (c *DoclingConverter) fetchStatus(ctx context.Context, correlationID, taskID string) (taskStatusResponse, []byte, error) {
	endpoint := fmt.Sprintf("%s%s%s", c.endpoint, doclingStatusPath, taskID)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return taskStatusResponse{}, nil, fmt.Errorf("build docling status request: %w", err)
	}

	req.Header.Set(correlationIDHeader, correlationID)
	req.Header.Set(endpointHeader, c.endpoint)
	if c.apiKey != "" {
		req.Header.Set(apiKeyHeader, c.apiKey)
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return taskStatusResponse{}, nil, fmt.Errorf("call docling status: %w", err)
	}
	defer resp.Body.Close()

	body, readErr := io.ReadAll(resp.Body)
	if readErr != nil {
		return taskStatusResponse{}, nil, fmt.Errorf("read docling status response: %w", readErr)
	}

	if resp.StatusCode != http.StatusOK {
		baseErr := fmt.Errorf("docling status returned %d: %s", resp.StatusCode, string(body))
		if isTemporaryDoclingStatus(resp.StatusCode) {
			return taskStatusResponse{}, body, &temporaryDoclingError{statusCode: resp.StatusCode, body: responseSnippet(body), cause: baseErr}
		}

		return taskStatusResponse{}, body, baseErr
	}

	var statusResp taskStatusResponse
	if err := json.Unmarshal(body, &statusResp); err != nil {
		return taskStatusResponse{}, body, fmt.Errorf("unmarshal docling status response: %w", err)
	}

	if statusResp.TaskID == "" {
		statusResp.TaskID = taskID
	}

	return statusResp, body, nil
}

// fetchResult читает ConvertDocumentResponse / другие варианты из /v1/result/{task_id}
func (c *DoclingConverter) fetchResult(ctx context.Context, correlationID, taskID string) (convertDocumentResponse, []byte, error) {
	endpoint := fmt.Sprintf("%s%s%s", c.endpoint, doclingResultPath, taskID)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return convertDocumentResponse{}, nil, fmt.Errorf("build docling result request: %w", err)
	}

	req.Header.Set(correlationIDHeader, correlationID)
	req.Header.Set(endpointHeader, c.endpoint)
	if c.apiKey != "" {
		req.Header.Set(apiKeyHeader, c.apiKey)
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return convertDocumentResponse{}, nil, fmt.Errorf("call docling result: %w", err)
	}
	defer resp.Body.Close()

	body, readErr := io.ReadAll(resp.Body)
	if readErr != nil {
		return convertDocumentResponse{}, nil, fmt.Errorf("read docling result response: %w", readErr)
	}

	if resp.StatusCode != http.StatusOK {
		return convertDocumentResponse{}, body, fmt.Errorf("docling result returned %d: %s", resp.StatusCode, string(body))
	}

	var resultResp convertDocumentResponse
	if err := json.Unmarshal(body, &resultResp); err != nil {
		return convertDocumentResponse{}, body, fmt.Errorf("unmarshal docling result response: %w", err)
	}

	return resultResp, body, nil
}

// buildConvertResult собирает domain.ConvertResult из ConvertDocumentResponse.
func buildConvertResult(resp convertDocumentResponse) (domain.ConvertResult, error) {
	normalizedStatus := normalizeStatus(resp.Status)
	if !isSuccessStatus(normalizedStatus) {
		return domain.ConvertResult{}, fmt.Errorf("docling response status=%s", resp.Status)
	}

	doc := resp.Document

	charset := ""
	if doc.Charset != nil {
		charset = *doc.Charset
	}

	if md, ok := doclingjson.RenderMarkdown(doc.JsonContent, doc.MdContent); ok && md != "" {
		return domain.ConvertResult{Content: []byte(md), MediaType: "text/markdown", Charset: charset}, nil
	}

	if md := firstNonEmpty(doc.MdContent); md != "" {
		return domain.ConvertResult{Content: []byte(md), MediaType: "text/markdown", Charset: charset}, nil
	}
	if html := firstNonEmpty(doc.HtmlContent); html != "" {
		return domain.ConvertResult{Content: []byte(html), MediaType: "text/html", Charset: charset}, nil
	}
	if text := firstNonEmpty(doc.TextContent); text != "" {
		return domain.ConvertResult{Content: []byte(text), MediaType: "text/plain", Charset: charset}, nil
	}

	return domain.ConvertResult{}, errors.New("docling response has empty content")
}

func firstNonEmpty(values ...*string) string {
	for _, v := range values {
		if v != nil && *v != "" {
			return *v
		}
	}
	return ""
}

func submitRetryDelay(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}

	delay := time.Duration(float64(submitRetryInitialWait) * math.Pow(submitRetryBackoff, float64(attempt-1)))
	if delay > submitRetryMaxWait {
		return submitRetryMaxWait
	}

	return delay
}

func isTemporaryDoclingError(err error) (bool, int) {
	var tmp domain.TemporaryError
	if errors.As(err, &tmp) && tmp.Temporary() {
		var code domain.StatusCoder
		if errors.As(err, &code) {
			return true, code.StatusCode()
		}

		return true, 0
	}

	return false, 0
}

func normalizeTesseractLang(lang string) string {
	lang = strings.ToLower(strings.TrimSpace(lang))
	switch lang {
	case "ru":
		return "rus"
	case "en":
		return "eng"
	default:
		return lang
	}
}

func boolPtr(v bool) *bool {
	return &v
}
