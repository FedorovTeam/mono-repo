package converter

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/mono-repo/docling-transformer-service/internal/telemetry"
)

const (
	doclingConvertPath  = "/convert"
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

// Converter определяет поведение конвертера документов.
type Converter interface {
	Convert(ctx context.Context, req DoclingConvertRequest) (string, error)
}

// DoclingConverter вызывает HTTP-обёртку Docling, прокидывая correlation-id.
type DoclingConverter struct {
	endpoint string
	client   *http.Client
}

// NewDoclingConverter создаёт конвертер с таймаутом по умолчанию.
func NewDoclingConverter(endpoint string) *DoclingConverter {
	return &DoclingConverter{
		endpoint: endpoint,
		client: &http.Client{
			Timeout: 15 * time.Second,
		},
	}
}

// Convert выполняет HTTP-вызов в Docling-сервис и возвращает Markdown.
func (c *DoclingConverter) Convert(ctx context.Context, req DoclingConvertRequest) (string, error) {
	if len(req.Content) == 0 {
		return "", errors.New("empty document")
	}

	ctx, correlationID := telemetry.EnsureCorrelationID(ctx)

	contentType := req.ContentType
	if contentType == "" {
		contentType = defaultContentType
	}

	endpoint := fmt.Sprintf("%s%s", c.endpoint, doclingConvertPath)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(req.Content))
	if err != nil {
		return "", fmt.Errorf("build docling request: %w", err)
	}

	httpReq.Header.Set("Content-Type", contentType)
	httpReq.Header.Set(correlationIDHeader, correlationID)
	if req.FileName != "" {
		httpReq.Header.Set(sourceFileHeader, req.FileName)
	}
	if c.endpoint != "" {
		httpReq.Header.Set(endpointHeader, c.endpoint)
	}

	resp, err := c.client.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("call docling api: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read docling response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("docling api returned status %d: %s", resp.StatusCode, string(body))
	}

	return string(body), nil
}
