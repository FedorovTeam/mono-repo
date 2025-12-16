package usecase

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/mono-repo/docling-transformer-service/internal/backoff"
	"github.com/mono-repo/docling-transformer-service/internal/domain"
	transformations "github.com/mono-repo/docling-transformer-service/internal/domain/transformations"
	repo "github.com/mono-repo/docling-transformer-service/internal/repository"
	"go.uber.org/zap"
)

var (
	ErrInvalidArgument   = errors.New("invalid argument")
	ErrQueueLimitReached = errors.New("queue limit reached")
	ErrNotFound          = errors.New("not found")
	ErrResultNotReady    = errors.New("conversion result not ready")
)

type TransformationConfig struct {
	MarkdownBucket    string
	QueueLimit        int
	StatusBackoff     backoff.Policy
	UseSourceAsync    bool
	DefaultOCREnabled bool
	DefaultOCRLang    string
	DefaultPDFBackend string
}

type SubmitConversionRequest struct {
	DocumentID        string
	OriginalBucket    string
	OriginalObjectKey string
	SourceBucket      string
	SourceObjectKey   string
	SourceURL         string
	MarkdownBucket    string
	MarkdownObjectKey string
	FilenameExtension string
	OCREnabled        *bool
	OCRLanguage       string
	PDFBackend        string
	CorrelationID     string
}

type SubmitConversionResult struct {
	Task              *transformations.TransformationTask
	MarkdownBucket    string
	MarkdownObjectKey string
}

type TransformerService struct {
	repo   repo.QueueRepository
	conv   domain.Converter
	cfg    TransformationConfig
	logger *zap.Logger
	client HTTPClient
}

type HTTPClient interface {
	Do(req *http.Request) (*http.Response, error)
}

func NewTransformerService(repo repo.QueueRepository, conv domain.Converter, cfg TransformationConfig, logger *zap.Logger, client HTTPClient) *TransformerService {
	if logger == nil {
		logger = zap.NewNop()
	}

	return &TransformerService{repo: repo, conv: conv, cfg: cfg, logger: logger, client: client}
}

func (uc *TransformerService) SubmitConversion(ctx context.Context, params SubmitConversionRequest) (SubmitConversionResult, error) {
	if params.DocumentID == "" {
		return SubmitConversionResult{}, fmt.Errorf("%w: document_id is required", ErrInvalidArgument)
	}
	if params.OriginalBucket == "" || params.OriginalObjectKey == "" {
		return SubmitConversionResult{}, fmt.Errorf("%w: original bucket and key are required", ErrInvalidArgument)
	}

	if params.SourceBucket != "" || params.SourceObjectKey != "" {
		if params.SourceBucket == "" || params.SourceObjectKey == "" {
			return SubmitConversionResult{}, fmt.Errorf("%w: both source bucket and key are required together", ErrInvalidArgument)
		}
	}

	if params.SourceURL != "" && (params.SourceBucket != "" || params.SourceObjectKey != "") {
		return SubmitConversionResult{}, fmt.Errorf("%w: provide either source_url or source bucket/object key", ErrInvalidArgument)
	}

	markdownBucket := params.MarkdownBucket
	if markdownBucket == "" {
		markdownBucket = uc.cfg.MarkdownBucket
	}
	if markdownBucket == "" {
		return SubmitConversionResult{}, fmt.Errorf("%w: markdown bucket is required", ErrInvalidArgument)
	}

	fileExt := domain.NormalizeExtension(params.FilenameExtension)
	if fileExt == "" {
		fileExt = domain.NormalizeExtension(filepath.Ext(params.OriginalObjectKey))
	}
	if fileExt == "" {
		return SubmitConversionResult{}, fmt.Errorf("%w: file extension is required", ErrInvalidArgument)
	}
	if _, err := domain.ResolveContentTypeByExtension(fileExt); err != nil {
		return SubmitConversionResult{}, fmt.Errorf("%w: %v", ErrInvalidArgument, err)
	}

	markdownKey := params.MarkdownObjectKey
	if markdownKey == "" {
		markdownKey = domain.BuildMarkdownKey(params.OriginalObjectKey)
	}

	ocrEnabled := params.OCREnabled
	if ocrEnabled == nil {
		ocrEnabled = boolPtr(uc.cfg.DefaultOCREnabled)
	}

	ocrLang := params.OCRLanguage
	if ocrLang == "" {
		ocrLang = uc.cfg.DefaultOCRLang
	}

	pdfBackend := params.PDFBackend
	if pdfBackend == "" {
		pdfBackend = uc.cfg.DefaultPDFBackend
	}

	existingTask, err := uc.repo.GetLatestByDocument(ctx, params.DocumentID)
	if err != nil {
		return SubmitConversionResult{}, fmt.Errorf("fetch latest task: %w", err)
	}

	if existingTask != nil && existingTask.Status != transformations.StatusFailed {
		result := SubmitConversionResult{Task: existingTask, MarkdownBucket: markdownBucket, MarkdownObjectKey: markdownKey}
		result.MarkdownBucket, result.MarkdownObjectKey = ResolveOutput(existingTask, markdownBucket, markdownKey)

		return result, nil
	}

	if uc.cfg.QueueLimit > 0 {
		pending, processing, waiting, err := uc.repo.CountByStatus(ctx)
		if err != nil {
			return SubmitConversionResult{}, fmt.Errorf("count queue tasks: %w", err)
		}

		if pending+processing+waiting >= uc.cfg.QueueLimit {
			return SubmitConversionResult{}, ErrQueueLimitReached
		}
	}

	queueID, err := uc.repo.Enqueue(ctx, transformations.EnqueueParams{
		DocumentID:        params.DocumentID,
		OriginalBucket:    params.OriginalBucket,
		OriginalObjectKey: params.OriginalObjectKey,
		SourceBucket:      params.SourceBucket,
		SourceObjectKey:   params.SourceObjectKey,
		SourceURL:         params.SourceURL,
		MarkdownBucket:    markdownBucket,
		MarkdownObjectKey: markdownKey,
		FilenameExtension: fileExt,
		OCREnabled:        ocrEnabled,
		OCRLanguage:       ocrLang,
		PDFBackend:        pdfBackend,
		CorrelationID:     params.CorrelationID,
	})
	if err != nil {
		return SubmitConversionResult{}, fmt.Errorf("enqueue transformation: %w", err)
	}

	result := SubmitConversionResult{
		Task: &transformations.TransformationTask{
			ID:                queueID,
			Status:            transformations.StatusPending,
			MarkdownBucket:    markdownBucket,
			MarkdownObjectKey: markdownKey,
			SourceBucket:      params.SourceBucket,
			SourceObjectKey:   params.SourceObjectKey,
			SourceURL:         params.SourceURL,
			OCREnabled:        ocrEnabled,
			OCRLanguage:       ocrLang,
			PDFBackend:        pdfBackend,
		},
		MarkdownBucket:    markdownBucket,
		MarkdownObjectKey: markdownKey,
	}

	return result, nil
}

func (uc *TransformerService) WaitForResult(ctx context.Context, queueID uuid.UUID) (*transformations.TransformationTask, error) {
	strategy := backoff.NewStrategy(uc.cfg.StatusBackoff)

	for attempt := 1; attempt <= uc.cfg.StatusBackoff.MaxAttempts; attempt++ {
		task, err := uc.repo.GetByID(ctx, queueID)
		if err != nil {
			return nil, fmt.Errorf("fetch task status: %w", err)
		}
		if task == nil {
			return nil, ErrNotFound
		}

		if task.Status == transformations.StatusDone || task.Status == transformations.StatusFailed {
			return task, nil
		}

		waitFor := strategy.NextInterval()
		if task.NextPollAt != nil {
			until := time.Until(*task.NextPollAt)
			if until > 0 {
				waitFor = until
			}
		}

		if attempt == uc.cfg.StatusBackoff.MaxAttempts {
			break
		}

		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(waitFor):
		}
	}

	return nil, ErrResultNotReady
}

func boolPtr(val bool) *bool {
	return &val
}

func (uc *TransformerService) ConvertDocxContent(ctx context.Context, filename string, content []byte) (domain.ConvertResult, error) {
	if uc.conv == nil {
		return domain.ConvertResult{}, fmt.Errorf("%w: converter is not configured", ErrInvalidArgument)
	}

	if len(content) == 0 {
		return domain.ConvertResult{}, fmt.Errorf("%w: empty content", ErrInvalidArgument)
	}

	name := strings.TrimSpace(filename)
	if name == "" {
		name = "document.docx"
	}

	contentType, err := domain.ResolveContentTypeByExtension(filepath.Ext(name))
	if err != nil {
		return domain.ConvertResult{}, fmt.Errorf("%w: %v", ErrInvalidArgument, err)
	}

	jobID, err := uc.conv.Submit(ctx, domain.ConvertRequest{FileName: name, Content: content, ContentType: contentType})
	if err != nil {
		return domain.ConvertResult{}, err
	}

	return uc.pollUntilReady(ctx, jobID)
}

func (uc *TransformerService) FetchContentByURL(ctx context.Context, rawURL string) ([]byte, error) {
	if rawURL == "" {
		return nil, fmt.Errorf("%w: docx_url is required when content is empty", ErrInvalidArgument)
	}

	if uc.client == nil {
		return nil, fmt.Errorf("%w: http client is not configured", ErrInvalidArgument)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, fmt.Errorf("%w: build request: %v", ErrInvalidArgument, err)
	}

	resp, err := uc.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch docx: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return nil, fmt.Errorf("%w: docx_url responded with %d: %s", ErrInvalidArgument, resp.StatusCode, string(body))
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read docx: %w", err)
	}

	if len(data) == 0 {
		return nil, fmt.Errorf("%w: docx_url returned empty body", ErrInvalidArgument)
	}

	return data, nil
}

func (uc *TransformerService) pollUntilReady(ctx context.Context, jobID string) (domain.ConvertResult, error) {
	strategy := backoff.NewStrategy(uc.cfg.StatusBackoff)

	for attempt := 1; attempt <= uc.cfg.StatusBackoff.MaxAttempts; attempt++ {
		statusResult, err := uc.conv.CheckStatus(ctx, jobID)
		if err != nil {
			return domain.ConvertResult{}, err
		}
		if statusResult.Result != nil {
			return *statusResult.Result, nil
		}

		if attempt == uc.cfg.StatusBackoff.MaxAttempts {
			break
		}

		select {
		case <-ctx.Done():
			return domain.ConvertResult{}, ctx.Err()
		case <-time.After(strategy.NextInterval()):
		}
	}

	return domain.ConvertResult{}, ErrResultNotReady
}

func ResolveOutput(task *transformations.TransformationTask, fallbackBucket, fallbackKey string) (string, string) {
	bucket := fallbackBucket
	key := fallbackKey

	if task != nil {
		if task.MarkdownBucket != "" {
			bucket = task.MarkdownBucket
		}
		if task.MarkdownObjectKey != "" {
			key = task.MarkdownObjectKey
		}
	}

	return bucket, key
}

func ResolveFilenameFromURL(rawURL string, fallback string) string {
	name := strings.TrimSpace(fallback)
	if name != "" {
		return name
	}

	parsed, err := url.Parse(rawURL)
	if err == nil {
		base := path.Base(parsed.Path)
		if base != "" && base != "/" {
			return base
		}
	}

	return "document.docx"
}
