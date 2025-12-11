package document

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"backend-document-service/internal/model"
)

type publishActivity interface {
	PublishMarkdown(ctx context.Context, input publishInput) error
}

// PublishRetrier повторно запускает публикацию в GitHub для документов,
// зависших в статусе Failed из-за временных ошибок GitHub.
type PublishRetrier struct {
	repo           Repository
	activities     publishActivity
	batchSize      int
	maxAttempts    int
	initialBackoff time.Duration
	maxBackoff     time.Duration
	now            func() time.Time
	logf           func(format string, args ...interface{})
	metrics        *workerMetrics
}

func NewPublishRetrier(repo Repository, activities publishActivity, batchSize, maxAttempts int, initialBackoff, maxBackoff time.Duration) *PublishRetrier {
	if batchSize <= 0 {
		batchSize = 100
	}
	if maxAttempts <= 0 {
		maxAttempts = 5
	}
	if initialBackoff <= 0 {
		initialBackoff = time.Minute
	}
	if maxBackoff <= 0 {
		maxBackoff = 30 * time.Minute
	}

	return &PublishRetrier{
		repo:           repo,
		activities:     activities,
		batchSize:      batchSize,
		maxAttempts:    maxAttempts,
		initialBackoff: initialBackoff,
		maxBackoff:     maxBackoff,
		now:            time.Now,
		logf:           log.Printf,
		metrics:        newWorkerMetrics("publish_retrier"),
	}
}

func (r *PublishRetrier) Run(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = time.Minute
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	r.reconcile(ctx)

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			r.reconcile(ctx)
		}
	}
}

func (r *PublishRetrier) reconcile(ctx context.Context) {
	status := model.StatusFailed
	offset := 0

	for {
		docs, err := r.repo.ListDocuments(ctx, r.batchSize, offset, &status)
		if err != nil {
			r.logf("publish-retrier: list documents: %v", err)
			r.metrics.recordError(ctx)
			return
		}
		r.metrics.observeBatch(ctx, len(docs))
		if len(docs) == 0 {
			r.metrics.observeBatch(ctx, 0)
			return
		}

		for i := range docs {
			doc := docs[i]
			start := time.Now()
			code := githubReasonCode(doc.ErrorReason)
			if code == "" {
				continue
			}
			if !strings.HasPrefix(code, publishGithubReasonPrefix) || !isRetryableGithubReason(code) {
				continue
			}

			attempts, err := r.repo.CountStatusLogsWithReasonPrefix(ctx, doc.ID, model.StatusFailed, publishGithubReasonPrefix)
			if err != nil {
				r.logf("publish-retrier: count attempts for document_id=%s: %v", doc.ID, err)
				continue
			}

			if attempts >= r.maxAttempts {
				r.markPermanent(ctx, doc, attempts)
				continue
			}

			backoff := r.backoffDuration(attempts)
			if r.now().Before(doc.UpdatedAt.Add(backoff)) {
				continue
			}

			if r.activities == nil {
				r.logf("publish-retrier: activities is nil, skip document_id=%s", doc.ID)
				continue
			}

			fullDoc, err := r.repo.GetDocument(ctx, doc.ID)
			if err != nil {
				r.logf("publish-retrier: load document %s: %v", doc.ID, err)
				continue
			}

			if fullDoc.MarkdownBucket == "" || resolvedMarkdownKey(fullDoc) == "" {
				reason := fmt.Sprintf("%s: markdown object unavailable", publishGithubPermanentReason)
				if _, err := r.repo.UpdateStatus(ctx, doc.ID, model.StatusFailed, reason, reason, nil); err != nil {
					r.logf("publish-retrier: mark missing markdown for %s: %v", doc.ID, err)
				}
				continue
			}

			input := publishInput{
				DocumentID:        doc.ID,
				DocumentName:      fullDoc.Name,
				Owner:             fullDoc.Owner,
				MarkdownBucket:    fullDoc.MarkdownBucket,
				MarkdownObjectKey: resolvedMarkdownKey(fullDoc),
				Repository:        fullDoc.Repository,
				Branch:            fullDoc.Branch,
				BaseBranch:        fullDoc.BaseBranch,
				Path:              fullDoc.Path,
				CreatePR:          fullDoc.CreatePR,
				CorrelationID:     correlationID(fullDoc),
			}

			if err := r.activities.PublishMarkdown(ctx, input); err != nil {
				r.logf("publish-retrier: publish document_id=%s correlation_id=%s attempt=%d/%d: %v", doc.ID, correlationID(fullDoc), attempts+1, r.maxAttempts, err)
				r.metrics.recordError(ctx)
				r.metrics.recordDuration(ctx, time.Since(start))
			} else {
				r.metrics.recordDuration(ctx, time.Since(start))
			}
		}

		if len(docs) < r.batchSize {
			return
		}
		offset += len(docs)
	}
}

func (r *PublishRetrier) backoffDuration(attempts int) time.Duration {
	if attempts < 1 {
		attempts = 1
	}

	d := r.initialBackoff
	for i := 1; i < attempts; i++ {
		d *= 2
		if d >= r.maxBackoff {
			return r.maxBackoff
		}
	}
	if d > r.maxBackoff {
		return r.maxBackoff
	}
	return d
}

func (r *PublishRetrier) markPermanent(ctx context.Context, doc model.Document, attempts int) {
	if !strings.HasPrefix(doc.ErrorReason, publishGithubPermanentReason) {
		reason := fmt.Sprintf("%s: max retry attempts reached (%d)", publishGithubPermanentReason, attempts)
		if _, err := r.repo.UpdateStatus(ctx, doc.ID, model.StatusFailed, reason, reason, nil); err != nil {
			r.logf("publish-retrier: mark permanent for %s: %v", doc.ID, err)
		}
	}
}

func githubReasonCode(reason string) string {
	if !strings.HasPrefix(reason, publishGithubReasonPrefix) {
		return ""
	}
	parts := strings.SplitN(reason, ":", 2)
	return strings.TrimSpace(parts[0])
}

func isRetryableGithubReason(code string) bool {
	switch code {
	case publishGithubTimeoutReason, publishGithub5xxReason, publishGithubRateLimitReason, publishGithubNetworkReason:
		return true
	}
	return false
}

func correlationID(doc *model.Document) string {
	if doc.WorkflowID != "" {
		return doc.WorkflowID
	}
	return doc.ID
}
