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

// ConvertRequest описывает входные данные для конвертации DOCX в Markdown.
type ConvertRequest struct {
	FileName    string
	Content     []byte
	ContentType string
}

// Converter определяет поведение конвертера документов.
type Converter interface {
	Convert(ctx context.Context, req ConvertRequest) (string, error)
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
func (c *DoclingConverter) Convert(ctx context.Context, req ConvertRequest) (string, error) {
	if len(req.Content) == 0 {
		return "", errors.New("empty document")
	}

	ctx, correlationID := telemetry.EnsureCorrelationID(ctx)

	contentType := req.ContentType
	if contentType == "" {
		contentType = "application/vnd.openxmlformats-officedocument.wordprocessingml.document"
	}

	endpoint := fmt.Sprintf("%s/convert", c.endpoint)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(req.Content))
	if err != nil {
		return "", fmt.Errorf("build request: %w", err)
	}

	httpReq.Header.Set("Content-Type", contentType)
	httpReq.Header.Set("X-Correlation-Id", correlationID)
	if req.FileName != "" {
		httpReq.Header.Set("X-Source-Filename", req.FileName)
	}
	if c.endpoint != "" {
		httpReq.Header.Set("X-Docling-Endpoint", c.endpoint)
	}

	resp, err := c.client.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("call docling endpoint: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read docling response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("docling returned status %d: %s", resp.StatusCode, string(body))
	}

	return string(body), nil
}
