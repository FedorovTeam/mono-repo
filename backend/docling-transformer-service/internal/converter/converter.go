package converter

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"time"

	"github.com/mono-repo/docling-transformer-service/internal/telemetry"
	"go.uber.org/zap"
)

const (
	doclingConvertPath  = "/v1/convert/file"
	defaultContentType  = "application/vnd.openxmlformats-officedocument.wordprocessingml.document"
	correlationIDHeader = "X-Correlation-Id"
	sourceFileHeader    = "X-Docling-Source-Filename"
	endpointHeader      = "X-Docling-Endpoint"
)

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

type doclingFileResponse struct {
	Document struct {
		Filename    string `json:"filename"`
		MdContent   string `json:"md_content"`
		HtmlContent string `json:"html_content"`
		TextContent string `json:"text_content"`
	} `json:"document"`
	Status         string  `json:"status"`
	Errors         []any   `json:"errors"`
	ProcessingTime float64 `json:"processing_time"`
}

// Converter определяет поведение конвертера документов.
type Converter interface {
	Convert(ctx context.Context, req DoclingConvertRequest) (DoclingConvertResult, error)
}

// DoclingConverter вызывает HTTP-обёртку Docling, прокидывая correlation-id.
type DoclingConverter struct {
	endpoint string
	client   *http.Client
	logger   *zap.Logger
}

// NewDoclingConverter создаёт конвертер с таймаутом по умолчанию.
func NewDoclingConverter(endpoint string, logger *zap.Logger) *DoclingConverter {
	if logger == nil {
		logger = zap.NewNop()
	}
	return &DoclingConverter{
		endpoint: endpoint,
		client: &http.Client{
			Timeout: 15 * time.Second,
		},
		logger: logger,
	}
}

// Convert выполняет HTTP-вызов в Docling-сервис и возвращает Markdown.
func (c *DoclingConverter) Convert(ctx context.Context, req DoclingConvertRequest) (DoclingConvertResult, error) {
	if len(req.Content) == 0 {
		return DoclingConvertResult{}, errors.New("empty document")
	}

	ctx, correlationID := telemetry.EnsureCorrelationID(ctx)

	filename := req.FileName
	if filename == "" {
		filename = "document.docx"
	}

	var lastErr error

	for attempt := 1; attempt <= 3; attempt++ {
		var buf bytes.Buffer
		writer := multipart.NewWriter(&buf)

		// Основной файл
		filePart, err := writer.CreateFormFile("files", filename)
		if err != nil {
			return DoclingConvertResult{}, fmt.Errorf("create multipart file: %w", err)
		}
		if _, err := filePart.Write(req.Content); err != nil {
			return DoclingConvertResult{}, fmt.Errorf("write multipart file: %w", err)
		}

		// Явно просим Markdown (опционально, но красиво)
		_ = writer.WriteField("to_formats", "md")

		if err := writer.Close(); err != nil {
			return DoclingConvertResult{}, fmt.Errorf("close multipart writer: %w", err)
		}

		endpoint := fmt.Sprintf("%s%s", c.endpoint, doclingConvertPath)

		httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, &buf)
		if err != nil {
			return DoclingConvertResult{}, fmt.Errorf("build docling request: %w", err)
		}

		httpReq.Header.Set("Content-Type", writer.FormDataContentType())
		httpReq.Header.Set(correlationIDHeader, correlationID)
		if filename != "" {
			httpReq.Header.Set(sourceFileHeader, filename)
		}
		if c.endpoint != "" {
			httpReq.Header.Set(endpointHeader, c.endpoint)
		}

		resp, err := c.client.Do(httpReq)
		if err != nil {
			lastErr = fmt.Errorf("call docling api: %w", err)
			c.logger.Error("docling request failed",
				zap.String("correlation_id", correlationID),
				zap.Int("attempt", attempt),
				zap.Error(err),
			)
		} else {
			body, readErr := io.ReadAll(resp.Body)
			resp.Body.Close()
			if readErr != nil {
				lastErr = fmt.Errorf("read docling response: %w", readErr)
				c.logger.Error("docling read failed",
					zap.String("correlation_id", correlationID),
					zap.Int("attempt", attempt),
					zap.Error(readErr),
				)
			} else if resp.StatusCode != http.StatusOK {
				lastErr = fmt.Errorf("docling api returned status %d: %s", resp.StatusCode, string(body))
				c.logger.Error("docling returned non-200",
					zap.String("correlation_id", correlationID),
					zap.Int("attempt", attempt),
					zap.Int("status", resp.StatusCode),
					zap.String("body", string(body)),
				)
			} else {
				var dres doclingFileResponse
				if err := json.Unmarshal(body, &dres); err != nil {
					lastErr = fmt.Errorf("unmarshal docling response: %w", err)
					c.logger.Error("docling json unmarshal failed",
						zap.String("correlation_id", correlationID),
						zap.Int("attempt", attempt),
						zap.Error(err),
					)
				} else if dres.Document.MdContent == "" {
					lastErr = fmt.Errorf("docling response has empty md_content, status=%s", dres.Status)
					c.logger.Error("docling empty md_content",
						zap.String("correlation_id", correlationID),
						zap.String("status", dres.Status),
					)
				} else {
					// Успех 🎉
					return DoclingConvertResult{
						Content:   []byte(dres.Document.MdContent),
						MediaType: "text/markdown",
					}, nil
				}
			}
		}

		if attempt < 3 {
			select {
			case <-ctx.Done():
				return DoclingConvertResult{}, ctx.Err()
			case <-time.After(time.Duration(attempt) * 300 * time.Millisecond):
			}
		}
	}

	return DoclingConvertResult{}, lastErr
}
