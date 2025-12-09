package document

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"backend-document-service/internal/model"

	"go.temporal.io/api/serviceerror"
	"go.temporal.io/sdk/client"
)

type WorkflowRetrier struct {
	repo      Repository
	starter   WorkflowStarter
	taskQueue string
	batchSize int
	maxTries  int
	logf      func(format string, args ...interface{})
}

func NewWorkflowRetrier(repo Repository, starter WorkflowStarter, taskQueue string, batchSize, maxTries int) *WorkflowRetrier {
	if batchSize <= 0 {
		batchSize = 100
	}
	if maxTries <= 0 {
		maxTries = 3
	}

	return &WorkflowRetrier{
		repo:      repo,
		starter:   starter,
		taskQueue: taskQueue,
		batchSize: batchSize,
		maxTries:  maxTries,
		logf:      log.Printf,
	}
}

func (r *WorkflowRetrier) Run(ctx context.Context, interval time.Duration) {
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

func (r *WorkflowRetrier) reconcile(ctx context.Context) {
	status := model.StatusFailed
	offset := 0

	for {
		docs, err := r.repo.ListDocuments(ctx, r.batchSize, offset, &status)
		if err != nil {
			r.logf("workflow-retrier: list documents: %v", err)
			return
		}
		if len(docs) == 0 {
			return
		}

		for i := range docs {
			doc := docs[i]
			if !strings.HasPrefix(strings.ToLower(doc.ErrorReason), startWorkflowErrorPrefix) {
				continue
			}
			if r.starter == nil {
				r.logf("workflow-retrier: temporal client is nil, skip document_id=%s correlation_id=%s", doc.ID, r.correlationID(doc))
				continue
			}

			attempts, err := r.repo.CountStatusLogsWithReasonPrefix(ctx, doc.ID, model.StatusFailed, startWorkflowErrorPrefix)
			if err != nil {
				r.logf("workflow-retrier: count attempts for document_id=%s correlation_id=%s: %v", doc.ID, r.correlationID(doc), err)
				continue
			}
			if attempts >= r.maxTries {
				r.logf("workflow-retrier: max attempts reached for document_id=%s correlation_id=%s attempts=%d", doc.ID, r.correlationID(doc), attempts)
				continue
			}

			r.retry(ctx, doc, attempts+1)
		}

		if len(docs) < r.batchSize {
			return
		}
		offset += len(docs)
	}
}

func (r *WorkflowRetrier) retry(ctx context.Context, doc model.Document, attempt int) {
	correlationID := r.correlationID(doc)
	r.logf("workflow-retrier: restarting workflow document_id=%s correlation_id=%s attempt=%d/%d", doc.ID, correlationID, attempt, r.maxTries)

	options := client.StartWorkflowOptions{ID: doc.ID, TaskQueue: r.taskQueue}
	run, err := r.starter.ExecuteWorkflow(ctx, options, DocumentProcessingWorkflow, WorkflowInput{DocumentID: doc.ID})
	if err != nil {
		var alreadyStarted *serviceerror.WorkflowExecutionAlreadyStarted
		if errors.As(err, &alreadyStarted) {
			r.logf("workflow-retrier: workflow already started document_id=%s correlation_id=%s", doc.ID, correlationID)
			return
		}

		reason := fmt.Sprintf("%s %v", startWorkflowErrorPrefix, err)
		_, _ = r.repo.UpdateStatus(ctx, doc.ID, model.StatusFailed, reason, reason, nil)
		r.logf("workflow-retrier: failed to start workflow document_id=%s correlation_id=%s: %v", doc.ID, correlationID, err)
		return
	}

	if _, err := r.repo.UpdateWorkflowID(ctx, doc.ID, run.GetID()); err != nil {
		r.logf("workflow-retrier: update workflow id for document_id=%s correlation_id=%s: %v", doc.ID, correlationID, err)
	}
}

func (r *WorkflowRetrier) correlationID(doc model.Document) string {
	if doc.WorkflowID != "" {
		return doc.WorkflowID
	}
	return doc.ID
}
