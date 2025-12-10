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
	"path/filepath"
	"strings"
	"time"

	"github.com/mono-repo/docling-transformer-service/internal/telemetry"
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

var extensionToContentType = map[string]string{
	"doc":  "application/msword",
	"docx": "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
	"pdf":  "application/pdf",
}

var contentTypeToExtension = map[string]string{
	"application/msword": "doc",
	"application/vnd.openxmlformats-officedocument.wordprocessingml.document": "docx",
	"application/pdf": "pdf",
}

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

var ErrDoclingPollingAttemptsExceeded = errors.New("docling polling attempts exceeded")

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

// ResolveContentTypeByExtension возвращает content-type для заданного расширения.
func ResolveContentTypeByExtension(fileExt string) (string, error) {
	normalized := strings.TrimPrefix(strings.ToLower(fileExt), ".")
	if normalized == "" {
		normalized = defaultExtension
	}

	contentType, ok := extensionToContentType[normalized]
	if !ok {
		return "", fmt.Errorf("unsupported file extension: %s", normalized)
	}

	return contentType, nil
}

// DoclingConvertRequest описывает входные данные для конвертации DOCX в Markdown.
type DoclingConvertRequest struct {
	FileName    string
	Content     []byte
	ContentType string
}

// DoclingConvertResult описывает результат преобразования Docling.
type DoclingConvertResult struct {
	Content   []byte
	MediaType string
}

// Config описывает параметры HTTP-клиента Docling.
type Config struct {
	Endpoint string
	APIKey   string
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

// ConvertDocumentResponse из /v1/result/{task_id} (вариант конвертации)
type convertDocumentResponse struct {
	Document       exportDocumentResponse `json:"document"`
	Status         string                 `json:"status"`
	ProcessingTime float64                `json:"processing_time"`
}

type exportDocumentResponse struct {
	Filename       string  `json:"filename"`
	MdContent      *string `json:"md_content"`
	JsonContent    any     `json:"json_content"`
	HtmlContent    *string `json:"html_content"`
	TextContent    *string `json:"text_content"`
	DoctagsContent *string `json:"doctags_content"`
}

type doclingSubmitResponse struct {
	JobID  string `json:"job_id"`
	TaskID string `json:"task_id"`
}

// StatusCheckResult содержит статус задачи Docling и, при завершении, результат конвертации.
type StatusCheckResult struct {
	Status string
	Result *DoclingConvertResult
}

// Converter определяет поведение конвертера документов.
type Converter interface {
	Submit(ctx context.Context, req DoclingConvertRequest) (string, error)
	CheckStatus(ctx context.Context, jobID string) (StatusCheckResult, error)
}

// DoclingConverter вызывает HTTP-обёртку Docling, прокидывая correlation-id.
type DoclingConverter struct {
	endpoint string
	apiKey   string
	client   *http.Client
	logger   *zap.Logger
}

// NewDoclingConverter создаёт конвертер с параметрами по умолчанию.
func NewDoclingConverter(cfg Config, logger *zap.Logger) *DoclingConverter {
	if logger == nil {
		logger = zap.NewNop()
	}

	return &DoclingConverter{
		endpoint: strings.TrimRight(cfg.Endpoint, "/"),
		apiKey:   cfg.APIKey,
		client: &http.Client{
			Timeout: 15 * time.Second,
		},
		logger: logger,
	}
}

func resolveContentTypeAndExt(reqContentType, fileName string) (string, string, error) {
	normalizedContentType := strings.ToLower(reqContentType)
	if ext, ok := contentTypeToExtension[normalizedContentType]; ok {
		return normalizedContentType, ext, nil
	}

	fileExt := strings.TrimPrefix(strings.ToLower(filepath.Ext(fileName)), ".")
	if fileExt != "" {
		if contentType, ok := extensionToContentType[fileExt]; ok {
			return contentType, fileExt, nil
		}
		return "", "", fmt.Errorf("unsupported file extension: %s", fileExt)
	}

	if normalizedContentType != "" {
		return "", "", fmt.Errorf("unsupported content type: %s", reqContentType)
	}

	return extensionToContentType[defaultExtension], defaultExtension, nil
}

// Submit отправляет документ в Docling и возвращает идентификатор задачи.
func (c *DoclingConverter) Submit(ctx context.Context, req DoclingConvertRequest) (string, error) {
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

	jobID, err := c.submitWithRetry(ctx, correlationID, filename, contentType, req.Content)
	if err != nil {
		return "", err
	}

	c.logger.Info("docling submit accepted",
		zap.String("job_id", jobID),
		zap.String("correlation_id", correlationID),
	)

	return jobID, nil
}

// CheckStatus делает одиночный запрос статуса Docling и возвращает результат при готовности.
func (c *DoclingConverter) CheckStatus(ctx context.Context, jobID string) (StatusCheckResult, error) {
	if jobID == "" {
		return StatusCheckResult{}, errors.New("empty job id")
	}

	ctx, correlationID := telemetry.EnsureCorrelationID(ctx)

	// 1. Спрашиваем статус задачи
	statusResp, body, err := c.fetchStatus(ctx, correlationID, jobID)
	if err != nil {
		return StatusCheckResult{}, err
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
		// 2. Если задача завершена успешно — забираем результат
		resultResp, resultBody, err := c.fetchResult(ctx, correlationID, jobID)
		if err != nil {
			return StatusCheckResult{}, fmt.Errorf("docling result fetch failed: %w (body=%s)", err, responseSnippet(resultBody))
		}

		result, buildErr := buildConvertResult(resultResp)
		if buildErr != nil {
			return StatusCheckResult{}, fmt.Errorf("docling result: %w", buildErr)
		}

		c.logger.Info("docling conversion completed",
			zap.String("job_id", jobID),
			zap.String("correlation_id", correlationID),
		)

		return StatusCheckResult{
			Status: normalizedStatus,
			Result: &result,
		}, nil

	case isPendingStatus(normalizedStatus):
		// 3. Задача ещё в обработке — просто возвращаем статус
		c.logger.Debug("docling still processing",
			zap.String("job_id", jobID),
			zap.String("correlation_id", correlationID),
			zap.String("status", normalizedStatus),
		)
		return StatusCheckResult{
			Status: normalizedStatus,
			Result: nil,
		}, nil

	default:
		// 4. Неуспешный финальный статус
		return StatusCheckResult{}, fmt.Errorf("docling status=%s for job %s: response=%s", rawStatus, jobID, responseSnippet(body))
	}
}

func (c *DoclingConverter) submitWithRetry(ctx context.Context, correlationID, filename, contentType string, content []byte) (string, error) {
	var lastErr error

	for attempt := 1; attempt <= submitRetryAttempts; attempt++ {
		jobID, err := c.submit(ctx, correlationID, filename, contentType, content)
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

func (c *DoclingConverter) submit(ctx context.Context, correlationID, filename, contentType string, content []byte) (string, error) {
	endpoint := fmt.Sprintf("%s%s", c.endpoint, doclingSubmitPath)

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
	if err == nil {
		err = writer.Close()
	}
	if err != nil {
		return "", fmt.Errorf("build docling multipart: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, payload)
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
		return taskStatusResponse{}, body, fmt.Errorf("docling status returned %d: %s", resp.StatusCode, string(body))
	}

	var statusResp taskStatusResponse
	if err := json.Unmarshal(body, &statusResp); err != nil {
		return taskStatusResponse{}, body, fmt.Errorf("unmarshal docling status response: %w", err)
	}

	// Подстрахуемся: если task_id не пришёл, проставим из jobID
	if statusResp.TaskID == "" {
		statusResp.TaskID = jobID
	}

	return statusResp, body, nil
}

// fetchResult читает ConvertDocumentResponse из /v1/result/{task_id}
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

// buildConvertResult собирает DoclingConvertResult из ConvertDocumentResponse.
func buildConvertResult(resp convertDocumentResponse) (DoclingConvertResult, error) {
	normalizedStatus := normalizeStatus(resp.Status)
	if !isSuccessStatus(normalizedStatus) {
		return DoclingConvertResult{}, fmt.Errorf("docling response status=%s", resp.Status)
	}

	doc := resp.Document

	if md := firstNonEmpty(doc.MdContent); md != "" {
		return DoclingConvertResult{Content: []byte(md), MediaType: "text/markdown"}, nil
	}
	if html := firstNonEmpty(doc.HtmlContent); html != "" {
		return DoclingConvertResult{Content: []byte(html), MediaType: "text/html"}, nil
	}
	if text := firstNonEmpty(doc.TextContent); text != "" {
		return DoclingConvertResult{Content: []byte(text), MediaType: "text/plain"}, nil
	}

	return DoclingConvertResult{}, errors.New("docling response has empty content")
}

func firstNonEmpty(values ...*string) string {
	for _, v := range values {
		if v != nil && *v != "" {
			return *v
		}
	}
	return ""
}
