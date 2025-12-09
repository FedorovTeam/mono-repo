package converter

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"path/filepath"
	"strings"
	"time"

	"github.com/mono-repo/docling-transformer-service/internal/telemetry"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.uber.org/zap"
)

const (
	doclingSubmitPath   = "/v1/convert/submit"
	doclingStatusPath   = "/v1/convert/status/"
	correlationIDHeader = "X-Correlation-Id"
	sourceFileHeader    = "X-Docling-Source-Filename"
	endpointHeader      = "X-Docling-Endpoint"
	apiKeyHeader        = "X-Api-Key"
)

const (
	defaultExtension    = "docx"
	defaultPollInterval = time.Second
	defaultPollTimeout  = 30 * time.Second
	submitRetryAttempts = 3
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
	Endpoint           string
	APIKey             string
	StatusPollInterval time.Duration
	StatusPollTimeout  time.Duration
}

type doclingStatusResponse struct {
	JobID    string `json:"job_id"`
	Document struct {
		Filename       string  `json:"filename"`
		MdContent      *string `json:"md_content"`
		HtmlContent    *string `json:"html_content"`
		TextContent    *string `json:"text_content"`
		DoctagsContent *string `json:"doctags_content"`
	} `json:"document"`
	Status         string  `json:"status"`
	Errors         []any   `json:"errors"`
	ProcessingTime float64 `json:"processing_time"`
}

type doclingSubmitResponse struct {
	JobID string `json:"job_id"`
}

type converterMetrics struct {
	waitHistogram metric.Float64Histogram
}

// Converter определяет поведение конвертера документов.
type Converter interface {
	Convert(ctx context.Context, req DoclingConvertRequest) (DoclingConvertResult, error)
}

// DoclingConverter вызывает HTTP-обёртку Docling, прокидывая correlation-id.
type DoclingConverter struct {
	endpoint           string
	apiKey             string
	client             *http.Client
	logger             *zap.Logger
	statusPollInterval time.Duration
	statusPollTimeout  time.Duration
	metrics            converterMetrics
}

// NewDoclingConverter создаёт конвертер с параметрами по умолчанию.
func NewDoclingConverter(cfg Config, logger *zap.Logger) *DoclingConverter {
	if logger == nil {
		logger = zap.NewNop()
	}

	pollInterval := cfg.StatusPollInterval
	if pollInterval <= 0 {
		pollInterval = defaultPollInterval
	}

	pollTimeout := cfg.StatusPollTimeout
	if pollTimeout <= 0 {
		pollTimeout = defaultPollTimeout
	}

	return &DoclingConverter{
		endpoint: strings.TrimRight(cfg.Endpoint, "/"),
		apiKey:   cfg.APIKey,
		client: &http.Client{
			Timeout: 15 * time.Second,
		},
		logger:             logger,
		statusPollInterval: pollInterval,
		statusPollTimeout:  pollTimeout,
		metrics:            newConverterMetrics(logger),
	}
}

func newConverterMetrics(logger *zap.Logger) converterMetrics {
	meter := otel.GetMeterProvider().Meter("docling-transformer-service/converter")
	histogram, err := meter.Float64Histogram("docling_converter_wait_duration_ms")
	if err != nil && logger != nil {
		logger.Warn("failed to create wait duration histogram", zap.Error(err))
	}

	return converterMetrics{waitHistogram: histogram}
}

func (m converterMetrics) observeWait(ctx context.Context, duration time.Duration, jobID string) {
	if m.waitHistogram == nil {
		return
	}

	m.waitHistogram.Record(ctx, float64(duration.Milliseconds()), metric.WithAttributes(attribute.String("job_id", jobID)))
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

// Convert отправляет документ в Docling через submit, затем опрашивает статус до успешного результата.
func (c *DoclingConverter) Convert(ctx context.Context, req DoclingConvertRequest) (DoclingConvertResult, error) {
	if len(req.Content) == 0 {
		return DoclingConvertResult{}, errors.New("empty document")
	}

	ctx, correlationID := telemetry.EnsureCorrelationID(ctx)

	contentType, fileExt, err := resolveContentTypeAndExt(req.ContentType, req.FileName)
	if err != nil {
		return DoclingConvertResult{}, err
	}

	filename := req.FileName
	if filename == "" {
		filename = fmt.Sprintf("document.%s", fileExt)
	}

	jobID, err := c.submitWithRetry(ctx, correlationID, filename, contentType, req.Content)
	if err != nil {
		return DoclingConvertResult{}, err
	}

	c.logger.Info("docling submit accepted",
		zap.String("job_id", jobID),
		zap.String("correlation_id", correlationID),
	)

	waitStart := time.Now()
	statusResponse, err := c.pollStatus(ctx, correlationID, jobID)
	waitDuration := time.Since(waitStart)
	if err != nil {
		c.logger.Error("docling status polling failed",
			zap.String("job_id", jobID),
			zap.String("correlation_id", correlationID),
			zap.Duration("wait_duration", waitDuration),
			zap.Error(err),
		)
		return DoclingConvertResult{}, err
	}

	c.metrics.observeWait(ctx, waitDuration, jobID)
	c.logger.Info("docling conversion completed",
		zap.String("job_id", jobID),
		zap.Duration("wait_duration", waitDuration),
	)

	return buildConvertResult(statusResponse)
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

	partHeaders := textproto.MIMEHeader{}
	partHeaders.Set("Content-Disposition", fmt.Sprintf(`form-data; name="files"; filename="%s"`, filename))
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

	if submitResp.JobID == "" {
		return "", errors.New("docling submit response has empty job_id")
	}

	return submitResp.JobID, nil
}

func (c *DoclingConverter) pollStatus(ctx context.Context, correlationID, jobID string) (doclingStatusResponse, error) {
	if jobID == "" {
		return doclingStatusResponse{}, errors.New("empty job id")
	}

	start := time.Now()

	for attempt := 1; ; attempt++ {
		statusResp, err := c.fetchStatus(ctx, correlationID, jobID)
		if err == nil {
			switch statusResp.Status {
			case "success", "partial_success":
				return statusResp, nil
			case "pending", "processing", "queued", "running":
			// continue polling
			default:
				return doclingStatusResponse{}, fmt.Errorf("docling status=%s for job %s", statusResp.Status, jobID)
			}
		} else {
			c.logger.Error("docling status request failed",
				zap.String("job_id", jobID),
				zap.Int("attempt", attempt),
				zap.Error(err),
			)
		}

		elapsed := time.Since(start)
		if elapsed >= c.statusPollTimeout {
			return doclingStatusResponse{}, fmt.Errorf("docling polling timeout for job %s", jobID)
		}

		backoff := c.statusPollInterval * time.Duration(math.Pow(2, float64(attempt-1)))
		remaining := c.statusPollTimeout - elapsed
		if backoff > remaining {
			backoff = remaining
		}

		select {
		case <-ctx.Done():
			return doclingStatusResponse{}, ctx.Err()
		case <-time.After(backoff):
		}
	}
}

func (c *DoclingConverter) fetchStatus(ctx context.Context, correlationID, jobID string) (doclingStatusResponse, error) {
	endpoint := fmt.Sprintf("%s%s%s", c.endpoint, doclingStatusPath, jobID)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return doclingStatusResponse{}, fmt.Errorf("build docling status request: %w", err)
	}

	req.Header.Set(correlationIDHeader, correlationID)
	req.Header.Set(endpointHeader, c.endpoint)
	if c.apiKey != "" {
		req.Header.Set(apiKeyHeader, c.apiKey)
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return doclingStatusResponse{}, fmt.Errorf("call docling status: %w", err)
	}
	defer resp.Body.Close()

	body, readErr := io.ReadAll(resp.Body)
	if readErr != nil {
		return doclingStatusResponse{}, fmt.Errorf("read docling status response: %w", readErr)
	}

	if resp.StatusCode != http.StatusOK {
		return doclingStatusResponse{}, fmt.Errorf("docling status returned %d: %s", resp.StatusCode, string(body))
	}

	var statusResp doclingStatusResponse
	if err := json.Unmarshal(body, &statusResp); err != nil {
		return doclingStatusResponse{}, fmt.Errorf("unmarshal docling status response: %w", err)
	}

	if statusResp.JobID == "" {
		statusResp.JobID = jobID
	}

	return statusResp, nil
}

func buildConvertResult(resp doclingStatusResponse) (DoclingConvertResult, error) {
	if resp.Status != "success" && resp.Status != "partial_success" {
		return DoclingConvertResult{}, fmt.Errorf("docling response status=%s", resp.Status)
	}

	if resp.Document.MdContent != nil && *resp.Document.MdContent != "" {
		return DoclingConvertResult{Content: []byte(*resp.Document.MdContent), MediaType: "text/markdown"}, nil
	}

	if resp.Document.HtmlContent != nil && *resp.Document.HtmlContent != "" {
		return DoclingConvertResult{Content: []byte(*resp.Document.HtmlContent), MediaType: "text/html"}, nil
	}

	return DoclingConvertResult{}, errors.New("docling response has empty content")
}
