package converter

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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
	doclingSubmitPath = "/v1/convert/file/async"
	// Поллинг статуса задачи
	doclingStatusPath = "/v1/status/poll/"
	// Получение результата задачи
	doclingResultPath = "/v1/result/"

	correlationIDHeader = "X-Correlation-Id"
	sourceFileHeader    = "X-Docling-Source-Filename"
	endpointHeader      = "X-Docling-Endpoint"
	apiKeyHeader        = "X-Api-Key"

	doclingFileFieldName = "files"

	defaultExtension    = "docx"
	submitRetryAttempts = 3

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

// DoclingConverter вызывает HTTP-обёртку Docling, прокидывая correlation-id.
type DoclingConverter struct {
	endpoint           string
	apiKey             string
	defaultLanguage    string
	pdfDefaultLanguage string
	pdfEnableOCR       bool
	client             *http.Client
	logger             *zap.Logger
}

// NewDoclingConverter создаёт конвертер с параметрами по умолчанию.
func NewDoclingConverter(cfg Config, logger *zap.Logger) *DoclingConverter {
	if logger == nil {
		logger = zap.NewNop()
	}

	return &DoclingConverter{
		endpoint:           strings.TrimRight(cfg.Endpoint, "/"),
		apiKey:             cfg.APIKey,
		defaultLanguage:    cfg.DefaultLanguage,
		pdfDefaultLanguage: cfg.PDFDefaultLanguage,
		pdfEnableOCR:       cfg.PDFEnableOCR,
		client: &http.Client{
			Timeout: 60 * time.Second,
		},
		logger: logger,
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

func (c *DoclingConverter) selectLanguage(contentType string) string {
	if contentType == "application/pdf" && c.pdfDefaultLanguage != "" {
		return c.pdfDefaultLanguage
	}
	return c.defaultLanguage
}

// Submit отправляет документ в Docling и возвращает идентификатор задачи.
func (c *DoclingConverter) Submit(ctx context.Context, req domain.ConvertRequest) (string, error) {
	if len(req.Content) == 0 {
		return "", errors.New("empty document")
	}

	ctx, correlationID := telemetry.EnsureCorrelationID(ctx)

	contentType, fileExt, err := resolveContentTypeAndExt(req.ContentType, req.FileName)
	if err != nil {
		return "", err
	}

	filename := req.FileName
	if filename == "" {
		filename = fmt.Sprintf("document.%s", fileExt)
	}

	jobID, err := c.submitWithRetry(ctx, correlationID, filename, contentType, fileExt, req.Content)
	if err != nil {
		return "", err
	}

	c.logger.Info("docling submit accepted",
		zap.String("job_id", jobID),
		zap.String("correlation_id", correlationID),
		zap.String("content_type", contentType),
		zap.String("file_ext", fileExt),
	)

	return jobID, nil
}

// CheckStatus делает одиночный запрос статуса Docling и возвращает результат при готовности.
func (c *DoclingConverter) CheckStatus(ctx context.Context, jobID string) (domain.StatusCheckResult, error) {
	if jobID == "" {
		return domain.StatusCheckResult{}, errors.New("empty job id")
	}

	ctx, correlationID := telemetry.EnsureCorrelationID(ctx)

	statusResp, body, err := c.fetchStatus(ctx, correlationID, jobID)
	if err != nil {
		return domain.StatusCheckResult{}, err
	}

	rawStatus := strings.TrimSpace(statusResp.TaskStatus)
	normalizedStatus := normalizeStatus(rawStatus)

	if normalizedStatus == "" {
		normalizedStatus = "pending"
		c.logger.Warn("docling status missing, defaulting to pending",
			zap.String("job_id", jobID),
			zap.String("correlation_id", correlationID),
		)
	}

	switch {
	case isSuccessStatus(normalizedStatus):
		resultResp, resultBody, err := c.fetchResult(ctx, correlationID, jobID)
		if err != nil {
			return domain.StatusCheckResult{}, fmt.Errorf("docling result fetch failed: %w (body=%s)", err, responseSnippet(resultBody))
		}

		result, buildErr := buildConvertResult(resultResp)
		if buildErr != nil {
			return domain.StatusCheckResult{}, fmt.Errorf("docling result: %w", buildErr)
		}

		c.logger.Info("docling conversion completed",
			zap.String("job_id", jobID),
			zap.String("correlation_id", correlationID),
		)

		return domain.StatusCheckResult{
			Status: normalizedStatus,
			Result: &result,
		}, nil

	case isPendingStatus(normalizedStatus):
		c.logger.Debug("docling still processing",
			zap.String("job_id", jobID),
			zap.String("correlation_id", correlationID),
			zap.String("status", normalizedStatus),
		)
		return domain.StatusCheckResult{
			Status: normalizedStatus,
			Result: nil,
		}, nil

	default:
		c.logger.Warn("docling reported failure status",
			zap.String("job_id", jobID),
			zap.String("correlation_id", correlationID),
			zap.String("status", rawStatus),
			zap.String("status_body", responseSnippet(body)),
		)

		if resultResp, resultBody, resErr := c.fetchResult(ctx, correlationID, jobID); resErr == nil {
			if len(resultResp.Errors) > 0 {
				for _, e := range resultResp.Errors {
					c.logger.Error("docling conversion error detail",
						zap.String("job_id", jobID),
						zap.String("correlation_id", correlationID),
						zap.String("component_type", e.ComponentType),
						zap.String("module_name", e.ModuleName),
						zap.String("error_message", e.ErrorMessage),
					)
				}
			} else {
				c.logger.Warn("docling result on failure has no errors",
					zap.String("job_id", jobID),
					zap.String("correlation_id", correlationID),
					zap.String("result_body", responseSnippet(resultBody)),
				)
			}
		} else {
			c.logger.Warn("docling result fetch on failure errored",
				zap.String("job_id", jobID),
				zap.String("correlation_id", correlationID),
				zap.Error(resErr),
			)
		}

		return domain.StatusCheckResult{}, fmt.Errorf("docling status=%s for job %s: response=%s", rawStatus, jobID, responseSnippet(body))
	}
}

// --- внутренняя отправка задач и опции Docling ---

func (c *DoclingConverter) submitWithRetry(
	ctx context.Context,
	correlationID, filename, contentType, fileExt string,
	content []byte,
) (string, error) {
	var lastErr error

	for attempt := 1; attempt <= submitRetryAttempts; attempt++ {
		jobID, err := c.submit(ctx, correlationID, filename, contentType, fileExt, content)
		if err == nil {
			return jobID, nil
		}

		lastErr = err
		c.logger.Error("docling submit failed",
			zap.String("correlation_id", correlationID),
			zap.Int("attempt", attempt),
			zap.Error(err),
		)

		if attempt < submitRetryAttempts {
			select {
			case <-ctx.Done():
				return "", ctx.Err()
			case <-time.After(time.Duration(attempt) * 300 * time.Millisecond):
			}
		}
	}

	return "", lastErr
}

// writeDoclingOptions — формирует form-data поля с опциями Docling Serve.
func (c *DoclingConverter) writeDoclingOptions(
	writer *multipart.Writer,
	contentType, fileExt string,
) error {
	// Всегда явно просим Markdown
	if err := writer.WriteField("to_formats", "md"); err != nil {
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
	if c.shouldEnableOCR(contentType) {
		if err := writer.WriteField("ocr", "true"); err != nil {
			return fmt.Errorf("write ocr: %w", err)
		}

		// Включаем OCR (Docling сам решит, когда его применять)
		if err := writer.WriteField("do_ocr", "true"); err != nil {
			return fmt.Errorf("write do_ocr: %w", err)
		}

		// НЕ форсим OCR — оставляем текстовый слой, если он нормальный
		// (force_ocr не пишем вообще)

		// Дадим Docling самому выбрать доступный движок
		if err := writer.WriteField("ocr_engine", "auto"); err != nil {
			return fmt.Errorf("write ocr_engine: %w", err)
		}

		// Язык OCR
		if lang := c.selectLanguage(contentType); lang != "" {
			if err := writer.WriteField("ocr_lang", lang); err != nil {
				return fmt.Errorf("write ocr_lang: %w", err)
			}
		}

		// Бэкенд парсера PDF
		if err := writer.WriteField("pdf_backend", "pypdfium2"); err != nil {
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

func (c *DoclingConverter) submit(
	ctx context.Context,
	correlationID, filename, contentType, fileExt string,
	content []byte,
) (string, error) {
	endpoint := fmt.Sprintf("%s%s", c.endpoint, doclingSubmitPath)

	submitURL, err := url.Parse(endpoint)
	if err != nil {
		return "", fmt.Errorf("parse docling submit endpoint: %w", err)
	}

	query := submitURL.Query()
	if lang := c.selectLanguage(contentType); lang != "" {
		query.Set("language", lang)
	}
	if c.shouldEnableOCR(contentType) {
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
		return "", fmt.Errorf("build docling multipart: could not format content disposition")
	}

	partHeaders := textproto.MIMEHeader{}
	partHeaders.Set("Content-Disposition", contentDisposition)
	partHeaders.Set("Content-Type", contentType)

	part, err := writer.CreatePart(partHeaders)
	if err == nil {
		_, err = part.Write(content)
	}
	if err != nil {
		return "", fmt.Errorf("build docling multipart: %w", err)
	}

	// Опции Docling Serve (форматы, OCR, язык, pdf_backend и т.п.)
	if err = c.writeDoclingOptions(writer, contentType, fileExt); err != nil {
		return "", err
	}

	if err = writer.Close(); err != nil {
		return "", fmt.Errorf("build docling multipart: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, submitURL.String(), payload)
	if err != nil {
		return "", fmt.Errorf("build docling request: %w", err)
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
		return "", fmt.Errorf("call docling submit: %w", err)
	}
	defer resp.Body.Close()

	body, readErr := io.ReadAll(resp.Body)
	if readErr != nil {
		return "", fmt.Errorf("read docling submit response: %w", readErr)
	}

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusAccepted {
		return "", fmt.Errorf("docling submit returned status %d: %s", resp.StatusCode, string(body))
	}

	var submitResp doclingSubmitResponse
	if err := json.Unmarshal(body, &submitResp); err != nil {
		return "", fmt.Errorf("unmarshal docling submit response: %w", err)
	}

	jobID := submitResp.JobID
	if jobID == "" {
		jobID = submitResp.TaskID
	}
	if jobID == "" {
		return "", errors.New("docling submit response has empty job_id")
	}

	return jobID, nil
}

// fetchStatus читает TaskStatusResponse из /v1/status/poll/{task_id}
func (c *DoclingConverter) fetchStatus(ctx context.Context, correlationID, jobID string) (taskStatusResponse, []byte, error) {
	endpoint := fmt.Sprintf("%s%s%s", c.endpoint, doclingStatusPath, jobID)

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
		if resp.StatusCode >= 500 && resp.StatusCode < 600 {
			return taskStatusResponse{}, body, &temporaryDoclingError{statusCode: resp.StatusCode, body: responseSnippet(body), cause: baseErr}
		}

		return taskStatusResponse{}, body, baseErr
	}

	var statusResp taskStatusResponse
	if err := json.Unmarshal(body, &statusResp); err != nil {
		return taskStatusResponse{}, body, fmt.Errorf("unmarshal docling status response: %w", err)
	}

	if statusResp.TaskID == "" {
		statusResp.TaskID = jobID
	}

	return statusResp, body, nil
}

// fetchResult читает ConvertDocumentResponse / другие варианты из /v1/result/{task_id}
func (c *DoclingConverter) fetchResult(ctx context.Context, correlationID, jobID string) (convertDocumentResponse, []byte, error) {
	endpoint := fmt.Sprintf("%s%s%s", c.endpoint, doclingResultPath, jobID)

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
