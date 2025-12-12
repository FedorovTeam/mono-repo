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
	"github.com/mono-repo/docling-transformer-service/internal/domain"
	"go.uber.org/zap"
)

var (
	ErrInvalidArgument   = errors.New("invalid argument")
	ErrQueueLimitReached = errors.New("queue limit reached")
	ErrNotFound          = errors.New("not found")
	ErrResultNotReady    = errors.New("conversion result not ready")
)

type TransformationConfig struct {
	MarkdownBucket  string
	QueueLimit      int
	PollInterval    time.Duration
	PollMaxInterval time.Duration
	PollAttempts    int
}

type ConvertDocumentParams struct {
	DocumentID        string
	OriginalBucket    string
	OriginalObjectKey string
	MarkdownBucket    string
	MarkdownObjectKey string
	FilenameExtension string
	WaitForResult     bool
	CorrelationID     string
}

type ConvertDocumentResult struct {
	Task              *domain.TransformationTask
	MarkdownBucket    string
	MarkdownObjectKey string
}

type TransformationUseCase struct {
	repo   domain.QueueRepository
	conv   domain.Converter
	cfg    TransformationConfig
	logger *zap.Logger
	client HTTPClient
}

type HTTPClient interface {
	Do(req *http.Request) (*http.Response, error)
}

func NewTransformationUseCase(repo domain.QueueRepository, conv domain.Converter, cfg TransformationConfig, logger *zap.Logger, client HTTPClient) *TransformationUseCase {
	if logger == nil {
		logger = zap.NewNop()
	}

	return &TransformationUseCase{repo: repo, conv: conv, cfg: cfg, logger: logger, client: client}
}

func (uc *TransformationUseCase) ConvertDocument(ctx context.Context, params ConvertDocumentParams) (ConvertDocumentResult, error) {
	if params.DocumentID == "" {
		return ConvertDocumentResult{}, fmt.Errorf("%w: document_id is required", ErrInvalidArgument)
	}
	if params.OriginalBucket == "" || params.OriginalObjectKey == "" {
		return ConvertDocumentResult{}, fmt.Errorf("%w: original bucket and key are required", ErrInvalidArgument)
	}

	markdownBucket := params.MarkdownBucket
	if markdownBucket == "" {
		markdownBucket = uc.cfg.MarkdownBucket
	}
	if markdownBucket == "" {
		return ConvertDocumentResult{}, fmt.Errorf("%w: markdown bucket is required", ErrInvalidArgument)
	}

	fileExt := domain.NormalizeExtension(params.FilenameExtension)
	if fileExt == "" {
		fileExt = domain.NormalizeExtension(filepath.Ext(params.OriginalObjectKey))
	}
	if fileExt == "" {
		return ConvertDocumentResult{}, fmt.Errorf("%w: file extension is required", ErrInvalidArgument)
	}
	if _, err := domain.ResolveContentTypeByExtension(fileExt); err != nil {
		return ConvertDocumentResult{}, fmt.Errorf("%w: %v", ErrInvalidArgument, err)
	}

	markdownKey := params.MarkdownObjectKey
	if markdownKey == "" {
		markdownKey = domain.BuildMarkdownKey(params.OriginalObjectKey)
	}

	existingTask, err := uc.repo.GetLatestByDocument(ctx, params.DocumentID)
	if err != nil {
		return ConvertDocumentResult{}, fmt.Errorf("fetch latest task: %w", err)
	}

	if existingTask != nil && existingTask.Status != domain.StatusFailed {
		result := ConvertDocumentResult{Task: existingTask, MarkdownBucket: markdownBucket, MarkdownObjectKey: markdownKey}
		result.MarkdownBucket, result.MarkdownObjectKey = resolveOutput(existingTask, markdownBucket, markdownKey)

		if params.WaitForResult {
			task, waitErr := uc.WaitForTask(ctx, existingTask.ID)
			if waitErr != nil {
				return ConvertDocumentResult{}, waitErr
			}

			result.Task = task
			result.MarkdownBucket, result.MarkdownObjectKey = resolveOutput(task, markdownBucket, markdownKey)
		}

		return result, nil
	}

	if uc.cfg.QueueLimit > 0 {
		pending, processing, waiting, err := uc.repo.CountByStatus(ctx)
		if err != nil {
			return ConvertDocumentResult{}, fmt.Errorf("count queue tasks: %w", err)
		}

		if pending+processing+waiting >= uc.cfg.QueueLimit {
			return ConvertDocumentResult{}, ErrQueueLimitReached
		}
	}

	queueID, err := uc.repo.Enqueue(ctx, domain.EnqueueParams{
		DocumentID:        params.DocumentID,
		OriginalBucket:    params.OriginalBucket,
		OriginalObjectKey: params.OriginalObjectKey,
		MarkdownBucket:    markdownBucket,
		MarkdownObjectKey: markdownKey,
		FilenameExtension: fileExt,
		CorrelationID:     params.CorrelationID,
	})
	if err != nil {
		return ConvertDocumentResult{}, fmt.Errorf("enqueue transformation: %w", err)
	}

	result := ConvertDocumentResult{
		Task:              &domain.TransformationTask{ID: queueID, Status: domain.StatusPending, MarkdownBucket: markdownBucket, MarkdownObjectKey: markdownKey},
		MarkdownBucket:    markdownBucket,
		MarkdownObjectKey: markdownKey,
	}

	if params.WaitForResult {
		task, waitErr := uc.WaitForTask(ctx, queueID)
		if waitErr != nil {
			return ConvertDocumentResult{}, waitErr
		}

		result.Task = task
		result.MarkdownBucket, result.MarkdownObjectKey = resolveOutput(task, markdownBucket, markdownKey)
	}

	return result, nil
}

func (uc *TransformationUseCase) WaitForTask(ctx context.Context, queueID uuid.UUID) (*domain.TransformationTask, error) {
	backoff := uc.cfg.PollInterval
	if backoff <= 0 {
		backoff = 2 * time.Second
	}
	maxInterval := uc.cfg.PollMaxInterval
	if maxInterval <= 0 {
		maxInterval = 10 * time.Second
	}

	attempts := uc.cfg.PollAttempts
	if attempts <= 0 {
		attempts = 10
	}

	for attempt := 1; attempt <= attempts; attempt++ {
		task, err := uc.repo.GetByID(ctx, queueID)
		if err != nil {
			return nil, fmt.Errorf("fetch task status: %w", err)
		}
		if task == nil {
			return nil, ErrNotFound
		}

		if task.Status == domain.StatusDone || task.Status == domain.StatusFailed {
			return task, nil
		}

		waitFor := backoff
		if task.NextPollAt != nil {
			until := time.Until(*task.NextPollAt)
			if until > 0 {
				waitFor = until
			}
		}

		if attempt == attempts {
			break
		}

		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(waitFor):
		}

		backoff = backoff * 2
		if backoff > maxInterval {
			backoff = maxInterval
		}
	}

	return nil, ErrResultNotReady
}

func (uc *TransformationUseCase) ConvertDocxContent(ctx context.Context, filename string, content []byte) (domain.ConvertResult, error) {
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

func (uc *TransformationUseCase) FetchContentByURL(ctx context.Context, rawURL string) ([]byte, error) {
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

func (uc *TransformationUseCase) pollUntilReady(ctx context.Context, jobID string) (domain.ConvertResult, error) {
	backoff := uc.cfg.PollInterval

	for attempt := 1; attempt <= uc.cfg.PollAttempts; attempt++ {
		statusResult, err := uc.conv.CheckStatus(ctx, jobID)
		if err != nil {
			return domain.ConvertResult{}, err
		}
		if statusResult.Result != nil {
			return *statusResult.Result, nil
		}

		if attempt == uc.cfg.PollAttempts {
			break
		}

		select {
		case <-ctx.Done():
			return domain.ConvertResult{}, ctx.Err()
		case <-time.After(backoff):
		}

		backoff = backoff * 2
		if uc.cfg.PollMaxInterval > 0 && backoff > uc.cfg.PollMaxInterval {
			backoff = uc.cfg.PollMaxInterval
		}
	}

	return domain.ConvertResult{}, ErrResultNotReady
}

func resolveOutput(task *domain.TransformationTask, fallbackBucket, fallbackKey string) (string, string) {
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
