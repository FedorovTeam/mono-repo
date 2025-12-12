package application

import (
	"context"
	"log"
	"time"

	"backend-storage-service/internal/domain"
)

// WorkerConfig описывает конфигурацию воркера.
type WorkerConfig struct {
	Stage        string
	PollInterval time.Duration
	MaxAttempts  int
	Backoff      time.Duration
}

// WorkerService реализует цикл обработки задач.
type WorkerService struct {
	cfg       WorkerConfig
	txManager TxManager
	jobs      ProcessingJobRepository
	documents DocumentRepository
	logger    *log.Logger
}

// NewWorkerService создаёт сервис обработки задач.
func NewWorkerService(cfg WorkerConfig, txManager TxManager, jobs ProcessingJobRepository, documents DocumentRepository, logger *log.Logger) *WorkerService {
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = 5 * time.Second
	}
	if cfg.MaxAttempts <= 0 {
		cfg.MaxAttempts = 3
	}
	if cfg.Backoff <= 0 {
		cfg.Backoff = 5 * time.Second
	}
	if cfg.Stage == "" {
		cfg.Stage = "store_original"
	}
	if logger == nil {
		logger = log.Default()
	}

	return &WorkerService{
		cfg:       cfg,
		txManager: txManager,
		jobs:      jobs,
		documents: documents,
		logger:    logger,
	}
}

// Run запускает основной цикл обработки.
func (w *WorkerService) Run(ctx context.Context) {
	ticker := time.NewTicker(w.cfg.PollInterval)
	defer ticker.Stop()

	w.processLoop(ctx)

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			w.processLoop(ctx)
		}
	}
}

func (w *WorkerService) processLoop(ctx context.Context) {
	for {
		tx, err := w.txManager.Begin(ctx)
		if err != nil {
			w.logger.Printf("worker: begin tx: %v", err)
			return
		}

		job, err := w.jobs.SelectPendingForStage(ctx, tx, w.cfg.Stage)
		if err != nil {
			_ = tx.Rollback(ctx)
			if err == ErrNoJobs {
				return
			}
			w.logger.Printf("worker: select job: %v", err)
			return
		}

		if err := w.handleJob(ctx, tx, job); err != nil {
			_ = tx.Rollback(ctx)
			w.logger.Printf("worker: handle job %s: %v", job.TaskID, err)
			continue
		}

		if err := tx.Commit(ctx); err != nil {
			w.logger.Printf("worker: commit job %s: %v", job.TaskID, err)
		}
	}
}

func (w *WorkerService) handleJob(ctx context.Context, tx Tx, job *domain.ProcessingJob) error {
	if err := w.jobs.MarkInProgress(ctx, tx, job.TaskID, w.cfg.Stage, "picked job"); err != nil {
		return err
	}

	if err := w.documents.MarkStatus(ctx, tx, job.DocumentID, domain.DocumentStatusStoredOriginal, job.Payload, "processed by storage worker"); err != nil {
		return w.jobs.RescheduleOrFail(ctx, tx, job, w.cfg.Stage, err, w.cfg.MaxAttempts, w.cfg.Backoff)
	}

	return w.jobs.MarkCompleted(ctx, tx, job.TaskID, w.cfg.Stage, "stored original")
}
