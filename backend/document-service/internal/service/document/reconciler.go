package document

import (
	"context"
	"log"
	"time"

	"backend-document-service/internal/domain"
	usecase "backend-document-service/internal/usecase/document"
)

type confirmUploadCaller interface {
	ConfirmUpload(ctx context.Context, in usecase.ConfirmUploadInput) (*domain.Document, error)
}

type UploadReconciler struct {
	repo          Repository
	confirmUpload confirmUploadCaller
	batchSize     int
	minAge        time.Duration
	now           func() time.Time
	metrics       *workerMetrics
}

func NewUploadReconciler(repo Repository, confirm confirmUploadCaller, batchSize int, minAge time.Duration) *UploadReconciler {
	if batchSize <= 0 {
		batchSize = 100
	}
	if minAge <= 0 {
		minAge = time.Minute
	}

	return &UploadReconciler{
		repo:          repo,
		confirmUpload: confirm,
		batchSize:     batchSize,
		minAge:        minAge,
		now:           time.Now,
		metrics:       newWorkerMetrics("upload_reconciler"),
	}
}

func (r *UploadReconciler) Run(ctx context.Context, interval time.Duration) {
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

func (r *UploadReconciler) reconcile(ctx context.Context) {
	docStatus := domain.DocumentStatusUploaded
	cutoff := r.now().Add(-r.minAge)
	offset := 0

	for {
		docs, err := r.repo.ListDocuments(ctx, r.batchSize, offset, &docStatus)
		if err != nil {
			log.Printf("reconciler: list documents: %v", err)
			r.metrics.recordError(ctx)
			return
		}
		r.metrics.observeBatch(ctx, len(docs))
		if len(docs) == 0 {
			r.metrics.observeBatch(ctx, 0)
			return
		}

		for _, doc := range docs {
			started := time.Now()
			if doc.UpdatedAt.After(cutoff) {
				continue
			}
			if doc.OriginalBucket == "" || doc.OriginalObjectKey == "" {
				continue
			}

			if _, err := r.confirmUpload.ConfirmUpload(ctx, usecase.ConfirmUploadInput{DocumentID: doc.ID, Bucket: doc.OriginalBucket, ObjectKey: doc.OriginalObjectKey}); err != nil {
				log.Printf("reconciler: confirm upload for %s: %v", doc.ID, err)
				r.metrics.recordError(ctx)
			}
			r.metrics.recordDuration(ctx, time.Since(started))
		}

		if len(docs) < r.batchSize {
			return
		}
		offset += len(docs)
	}
}
