package callback

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"preprocess-service/internal/usecase/ports"

	"go.uber.org/zap"
)

type Client struct {
	http   *http.Client
	logger *zap.Logger
}

func NewClient(httpClient *http.Client, logger *zap.Logger) *Client {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 15 * time.Second}
	}

	if logger == nil {
		logger = zap.NewNop()
	}

	return &Client{
		http:   httpClient,
		logger: logger,
	}
}

func (c *Client) Send(ctx context.Context, url string, payload ports.CallbackPayload, attempt int) (bool, error) {
	if attempt <= 0 {
		attempt = 1
	}

	start := time.Now()
	logger := c.logger.With(
		zap.String("url", url),
		zap.Int("attempt", attempt),
	)
	logger.Info("sending callback request")

	body, err := json.Marshal(payload)
	if err != nil {
		logger.Error("failed to marshal callback payload",
			zap.Duration("duration", time.Since(start)),
			zap.Error(err),
		)
		return false, fmt.Errorf("marshal callback payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		logger.Error("failed to build callback request",
			zap.Duration("duration", time.Since(start)),
			zap.Error(err),
		)
		return false, fmt.Errorf("build callback request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		logger.Error("callback request failed",
			zap.Duration("duration", time.Since(start)),
			zap.Error(err),
		)
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return false, err
		}
		return true, fmt.Errorf("send callback: %w", err)
	}
	defer resp.Body.Close()

	io.Copy(io.Discard, resp.Body)

	logger.Info("callback response received",
		zap.Duration("duration", time.Since(start)),
		zap.Int("status_code", resp.StatusCode),
		zap.String("status", resp.Status),
	)

	if resp.StatusCode >= http.StatusOK && resp.StatusCode < http.StatusMultipleChoices {
		return false, nil
	}

	if resp.StatusCode >= http.StatusInternalServerError {
		return true, fmt.Errorf("callback responded with status %d", resp.StatusCode)
	}

	return false, fmt.Errorf("callback responded with status %d", resp.StatusCode)
}
