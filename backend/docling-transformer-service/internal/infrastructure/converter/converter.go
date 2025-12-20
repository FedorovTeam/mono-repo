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

func formatDoclingErrors(items []errorItem) string {
	var parts []string
	for _, item := range items {
		module := strings.TrimSpace(item.ModuleName)
		component := strings.TrimSpace(item.ComponentType)
		msg := strings.TrimSpace(item.ErrorMessage)

		var prefix string
		switch {
		case module != "" && component != "":
			prefix = fmt.Sprintf("%s/%s", component, module)
		case module != "":
			prefix = module
		case component != "":
			prefix = component
		}

		switch {
		case prefix != "" && msg != "":
			parts = append(parts, fmt.Sprintf("%s: %s", prefix, msg))
		case msg != "":
			parts = append(parts, msg)
		case prefix != "":
			parts = append(parts, prefix)
		}
	}

	return strings.Join(parts, "; ")
}

func doclingFailureDetails(statusBody []byte, resultResp convertDocumentResponse, resultBody []byte, resultErr error) string {
	var details []string

	if formatted := formatDoclingErrors(resultResp.Errors); formatted != "" {
		details = append(details, formatted)
	}

	if snippet := responseSnippet(resultBody); snippet != "" {
		details = append(details, fmt.Sprintf("result=%s", snippet))
	}

	if resultErr != nil {
		details = append(details, fmt.Sprintf("result_error=%v", resultErr))
	}

	if snippet := responseSnippet(statusBody); snippet != "" {
		details = append(details, fmt.Sprintf("status_response=%s", snippet))
	}

	return strings.Join(details, "; ")
}

// Config описывает параметры HTTP-клиента Docling.
type Config struct {
	Endpoint         string
	APIKey           string
	DefaultLanguage  string
	OCREngine        string
	OCRLang          []string
	SkipOCROnTextPDF *bool
	Mode             string
	Storage          StorageConfig
	TraceHttpRequest bool
	Render           RenderConfig
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

// RenderConfig управляет источниками данных для результата Docling.
type RenderConfig struct {
	PreferJSON bool
}

type storageSettings struct {
	Endpoint  string
	AccessKey string
	SecretKey string
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

type doclingOptions struct {
	ToFormats   []string `json:"to_formats,omitempty"`
	FromFormats []string `json:"from_formats,omitempty"`
	OCREngine   string   `json:"ocr_engine,omitempty"`
	OCRLang     []string `json:"ocr_lang,omitempty"`
}

// DoclingConverter вызывает HTTP-обёртку Docling, прокидывая correlation-id.
type DoclingConverter struct {
	endpoint         string
	apiKey           string
	defaultLanguage  string
	ocrEngine        string
	ocrLang          []string
	skipOCROnTextPDF bool
	mode             string
	storage          StorageConfig
	client           *http.Client
	logger           *zap.Logger
	render           RenderConfig
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

	ocrEngine := strings.TrimSpace(cfg.OCREngine)
	if ocrEngine == "" {
		ocrEngine = "easyocr"
	}

	ocrLang := cfg.OCRLang
	if ocrLang == nil && cfg.DefaultLanguage != "" {
		ocrLang = []string{cfg.DefaultLanguage}
	}

	skipOCROnTextPDF := true
	if cfg.SkipOCROnTextPDF != nil {
		skipOCROnTextPDF = *cfg.SkipOCROnTextPDF
	}

	httpClient := &http.Client{Timeout: 60 * time.Second}
	if cfg.TraceHttpRequest {
		httpClient.Transport = newLoggingTransport(httpClient.Transport, logger)
	}

	return &DoclingConverter{
		endpoint:         strings.TrimRight(cfg.Endpoint, "/"),
		apiKey:           cfg.APIKey,
		defaultLanguage:  cfg.DefaultLanguage,
		mode:             mode,
		storage:          cfg.Storage,
		client:           httpClient,
		logger:           logger,
		render:           cfg.Render,
		ocrEngine:        ocrEngine,
		ocrLang:          append([]string(nil), ocrLang...),
		skipOCROnTextPDF: skipOCROnTextPDF,
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

func (c *DoclingConverter) selectLanguage() string {
	return c.defaultLanguage
}

func isPDFContent(contentType, fileExt string) bool {
	if strings.EqualFold(contentType, "application/pdf") {
		return true
	}

	return strings.EqualFold(fileExt, "pdf")
}

func detectPDFHasTextLayer(content []byte) bool {
	const maxScanBytes = 512 * 1024

	if len(content) == 0 {
		return false
	}

	if len(content) > maxScanBytes {
		content = content[:maxScanBytes]
	}

	lower := bytes.ToLower(content)

	if !bytes.HasPrefix(lower, []byte("%pdf")) && !bytes.Contains(lower, []byte("%pdf")) {
		return false
	}

	if bytes.Contains(lower, []byte("/font")) {
		return true
	}

	if bytes.Contains(lower, []byte("bt")) && bytes.Contains(lower, []byte("et")) && bytes.Contains(content, []byte("(")) {
		return true
	}

	if bytes.Contains(lower, []byte("/contents")) && bytes.Contains(lower, []byte("text")) {
		return true
	}

	return false
}

func (c *DoclingConverter) shouldUseOCROptions(contentType, fileExt string, content []byte) bool {
	if !c.skipOCROnTextPDF {
		return true
	}

	if !isPDFContent(contentType, fileExt) {
		return true
	}

	if len(content) == 0 {
		return true
	}

	if detectPDFHasTextLayer(content) {
		c.logger.Debug("text layer detected, skipping ocr options",
			zap.String("content_type", contentType),
			zap.String("file_ext", fileExt),
		)
		return false
	}

	return true
}

func (c *DoclingConverter) ocrLanguages() []string {
	if len(c.ocrLang) == 0 {
		return nil
	}

	langs := make([]string, 0, len(c.ocrLang))
	for _, lang := range c.ocrLang {
		lang = strings.TrimSpace(lang)
		if lang == "" {
			continue
		}
		langs = append(langs, lang)
	}

	if len(langs) == 0 {
		return nil
	}

	return langs
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

	switch c.mode {
	case doclingModeSourceAsync:
		useOCR := c.shouldUseOCROptions(contentType, fileExt, req.Content)
		ids, err := c.submitSourceAsyncWithRetry(ctx, correlationID, filename, contentType, fileExt, req, useOCR)
		if err != nil {
			return domain.DoclingIdentifiers{}, err
		}

		c.logger.Info("docling submit accepted",
			zap.String("docling_task_id", ids.TaskID),
			zap.String("docling_job_id", ids.JobID),
			zap.String("correlation_id", correlationID),
			zap.String("content_type", contentType),
			zap.String("file_ext", fileExt),
		)

		return ids, nil

	default:
		if len(req.Content) == 0 {
			return domain.DoclingIdentifiers{}, errors.New("empty document")
		}

		useOCR := c.shouldUseOCROptions(contentType, fileExt, req.Content)
		ids, err := c.submitWithRetry(ctx, correlationID, filename, contentType, fileExt, req.Content, useOCR)
		if err != nil {
			return domain.DoclingIdentifiers{}, err
		}

		c.logger.Info("docling submit accepted",
			zap.String("docling_task_id", ids.TaskID),
			zap.String("docling_job_id", ids.JobID),
			zap.String("correlation_id", correlationID),
			zap.String("content_type", contentType),
			zap.String("file_ext", fileExt),
		)

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

		result, buildErr := c.buildConvertResult(resultResp)
		if buildErr != nil {
			return domain.StatusCheckResult{}, fmt.Errorf("docling result: %w", buildErr)
		}

		c.logger.Info("docling conversion completed", logFields...)

		return domain.StatusCheckResult{Status: normalizedStatus, Result: &result}, nil

	case isPendingStatus(normalizedStatus):
		c.logger.Debug("docling still processing", append(logFields, zap.String("status", normalizedStatus))...)
		return domain.StatusCheckResult{Status: normalizedStatus, Result: nil}, nil

	default:
		resultResp, resultBody, resultErr := c.fetchResult(ctx, correlationID, taskID)
		if resultErr != nil {
			c.logger.Warn("docling result fetch after failure status errored", append(logFields,
				zap.String("status", rawStatus),
				zap.String("status_body", responseSnippet(body)),
				zap.Error(resultErr),
			)...)
		}

		details := doclingFailureDetails(body, resultResp, resultBody, resultErr)
		if details == "" {
			details = responseSnippet(body)
		}

		return domain.StatusCheckResult{}, &domain.DoclingFailureError{
			TaskID:  taskID,
			Status:  rawStatus,
			Details: details,
		}
	}
}

// --- внутренняя отправка задач и опции Docling ---

func (c *DoclingConverter) submitWithRetry(
	ctx context.Context,
	correlationID, filename, contentType, fileExt string,
	content []byte,
	useOCR bool,
) (domain.DoclingIdentifiers, error) {
	var lastErr error

	for attempt := 1; attempt <= submitRetryAttempts; attempt++ {
		ids, err := c.submit(ctx, correlationID, filename, contentType, fileExt, content, useOCR)
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
	useOCR bool,
) (domain.DoclingIdentifiers, error) {
	var lastErr error

	for attempt := 1; attempt <= submitRetryAttempts; attempt++ {
		ids, err := c.submitSourceAsync(ctx, correlationID, filename, contentType, fileExt, req, useOCR)
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

// writeDoclingOptions — form-data поля под OpenAPI:
// - to_formats: повторяем ключ для массива
func (c *DoclingConverter) writeDoclingOptions(
	writer *multipart.Writer,
	fileExt string,
	useOCR bool,
) error {
	if useOCR {
		if c.ocrEngine != "" {
			if err := writer.WriteField("ocr_engine", c.ocrEngine); err != nil {
				return fmt.Errorf("write ocr_engine: %w", err)
			}
		}

		for _, lang := range c.ocrLanguages() {
			if err := writer.WriteField("ocr_lang", lang); err != nil {
				return fmt.Errorf("write ocr_lang: %w", err)
			}
		}
	}

	// to_formats[]: IMPORTANT — не "json,md", а отдельными значениями
	if err := writer.WriteField("to_formats", "md"); err != nil {
		return fmt.Errorf("write to_formats md: %w", err)
	}

	// from_formats: тоже array, но нам достаточно одного значения
	if fileExt != "" {
		if err := writer.WriteField("from_formats", fileExt); err != nil {
			return fmt.Errorf("write from_formats: %w", err)
		}
	}

	return nil
}

func (c *DoclingConverter) buildDoclingOptions(fileExt string, useOCR bool) doclingOptions {
	opts := doclingOptions{
		ToFormats: []string{"md"},
	}

	if useOCR {
		opts.OCREngine = c.ocrEngine

		if langs := c.ocrLanguages(); len(langs) > 0 {
			opts.OCRLang = langs
		}
	}

	if fileExt != "" {
		opts.FromFormats = []string{fileExt}
	}

	return opts
}

func (c *DoclingConverter) submit(
	ctx context.Context,
	correlationID, filename, contentType, fileExt string,
	content []byte,
	useOCR bool,
) (domain.DoclingIdentifiers, error) {
	endpoint := fmt.Sprintf("%s%s", c.endpoint, doclingSubmitPath)

	// В OpenAPI у /v1/convert/file/async НЕТ query параметров language/ocr.
	submitURL, err := url.Parse(endpoint)
	if err != nil {
		return domain.DoclingIdentifiers{}, fmt.Errorf("parse docling submit endpoint: %w", err)
	}

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

	language := c.selectLanguage()
	if language != "" {
		if err := writer.WriteField("language", language); err != nil {
			return domain.DoclingIdentifiers{}, fmt.Errorf("write language: %w", err)
		}
	}

	if err = c.writeDoclingOptions(writer, fileExt, useOCR); err != nil {
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

	var statusResp doclingSubmitResponse
	if err := json.Unmarshal(body, &statusResp); err != nil {
		return domain.DoclingIdentifiers{}, fmt.Errorf("unmarshal docling submit response: %w", err)
	}

	ids := domain.DoclingIdentifiers{TaskID: statusResp.TaskID, JobID: statusResp.JobID}
	if ids.TaskID == "" {
		return domain.DoclingIdentifiers{}, errors.New("docling submit response has empty task identifier")
	}
	// В TaskStatusResponse нет job_id в OpenAPI 1.9.0
	return ids, nil
}

func (c *DoclingConverter) submitSourceAsync(
	ctx context.Context,
	correlationID, filename, contentType, fileExt string,
	req domain.ConvertRequest,
	useOCR bool,
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
		Options: c.buildDoclingOptions(fileExt, useOCR),
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

	var statusResp doclingSubmitResponse
	if err := json.Unmarshal(body, &statusResp); err != nil {
		return domain.DoclingIdentifiers{}, fmt.Errorf("unmarshal docling source submit response: %w", err)
	}

	ids := domain.DoclingIdentifiers{TaskID: statusResp.TaskID, JobID: statusResp.JobID}
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

// fetchResult читает ConvertDocumentResponse из /v1/result/{task_id}
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
func (c *DoclingConverter) buildConvertResult(resp convertDocumentResponse) (domain.ConvertResult, error) {
	normalizedStatus := normalizeStatus(resp.Status)
	if !isSuccessStatus(normalizedStatus) {
		return domain.ConvertResult{}, fmt.Errorf("docling response status=%s", resp.Status)
	}

	doc := resp.Document

	charset := ""
	if doc.Charset != nil {
		charset = *doc.Charset
	}

	markdown := c.selectMarkdown(doc)
	if markdown != "" {
		return domain.ConvertResult{Content: []byte(markdown), MediaType: "text/markdown", Charset: charset}, nil
	}
	if html := firstNonEmpty(doc.HtmlContent); html != "" {
		return domain.ConvertResult{Content: []byte(html), MediaType: "text/html", Charset: charset}, nil
	}
	if text := firstNonEmpty(doc.TextContent); text != "" {
		return domain.ConvertResult{Content: []byte(text), MediaType: "text/plain", Charset: charset}, nil
	}

	return domain.ConvertResult{}, errors.New("docling response has empty content")
}

func (c *DoclingConverter) selectMarkdown(doc exportDocumentResponse) string {
	mdContent := strings.TrimSpace(firstNonEmpty(doc.MdContent))
	if mdContent != "" {
		return mdContent
	}
	return ""
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
