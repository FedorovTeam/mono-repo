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
	"net/textproto"
	"path/filepath"
	"strings"
	"time"

	"github.com/mono-repo/docling-transformer-service/internal/telemetry"
	"go.uber.org/zap"
)

const (
	doclingConvertPath  = "/v1/convert/file"
	correlationIDHeader = "X-Correlation-Id"
	sourceFileHeader    = "X-Docling-Source-Filename"
	endpointHeader      = "X-Docling-Endpoint"
	apiKeyHeader        = "X-Api-Key"
)

const defaultExtension = "docx"

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

// Converter определяет поведение конвертера документов.
type Converter interface {
	Convert(ctx context.Context, req DoclingConvertRequest) (DoclingConvertResult, error)
}

// DoclingConverter вызывает HTTP-обёртку Docling, прокидывая correlation-id.
type DoclingConverter struct {
	endpoint string
	apiKey   string
	client   *http.Client
	logger   *zap.Logger
}

// NewDoclingConverter создаёт конвертер с таймаутом по умолчанию.
func NewDoclingConverter(endpoint, apiKey string, logger *zap.Logger) *DoclingConverter {
	if logger == nil {
		logger = zap.NewNop()
	}
	return &DoclingConverter{
		endpoint: endpoint,
		apiKey:   apiKey,
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

// Convert выполняет HTTP-вызов в Docling-сервис и возвращает Markdown.
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

	var lastErr error

	for attempt := 1; attempt <= 3; attempt++ {
		endpoint := fmt.Sprintf("%s%s", c.endpoint, doclingConvertPath)

		payload := &bytes.Buffer{}
		writer := multipart.NewWriter(payload)

		partHeaders := textproto.MIMEHeader{}
		partHeaders.Set("Content-Disposition", fmt.Sprintf(`form-data; name="files"; filename="%s"`, filename))
		partHeaders.Set("Content-Type", contentType)

		part, err := writer.CreatePart(partHeaders)
		if err == nil {
			_, err = part.Write(req.Content)
		}
		if err == nil {
			err = writer.Close()
		}
		if err != nil {
			return DoclingConvertResult{}, fmt.Errorf("build docling multipart: %w", err)
		}

		httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, payload)
		if err != nil {
			return DoclingConvertResult{}, fmt.Errorf("build docling request: %w", err)
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
				mediaType := resp.Header.Get("Content-Type")
				if mediaType == "" {
					mediaType = "text/markdown"
				}

				if strings.Contains(mediaType, "application/json") {
					var dres doclingFileResponse
					if err := json.Unmarshal(body, &dres); err != nil {
						lastErr = fmt.Errorf("unmarshal docling response: %w", err)
						c.logger.Error("docling json unmarshal failed",
							zap.String("correlation_id", correlationID),
							zap.Int("attempt", attempt),
							zap.Error(err),
						)
					} else {
						if dres.Status != "success" && dres.Status != "partial_success" {
							lastErr = fmt.Errorf("docling response status=%s", dres.Status)
							c.logger.Error("docling returned non-success status",
								zap.String("correlation_id", correlationID),
								zap.String("status", dres.Status),
								zap.Any("errors", dres.Errors),
							)
							continue
						}

						if dres.Document.MdContent == nil || *dres.Document.MdContent == "" {
							lastErr = fmt.Errorf("docling response has empty md_content, status=%s", dres.Status)
							c.logger.Error("docling empty md_content",
								zap.String("correlation_id", correlationID),
								zap.String("status", dres.Status),
							)
						} else {
							return DoclingConvertResult{
								Content:   []byte(*dres.Document.MdContent),
								MediaType: "text/markdown",
							}, nil
						}
					}
				} else {
					return DoclingConvertResult{Content: body, MediaType: mediaType}, nil
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
