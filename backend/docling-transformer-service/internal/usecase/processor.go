package usecase

import (
	"context"
	"sync"
	"time"

	"github.com/mono-repo/docling-transformer-service/internal/domain"
	repo "github.com/mono-repo/docling-transformer-service/internal/repository"
	"github.com/mono-repo/docling-transformer-service/internal/usecase/metrics"
	"go.uber.org/zap"
)

// Config задаёт параметры воркера очереди.
type Config struct {
	MarkdownBucket         string
	MaxWorkers             int
	PollInterval           time.Duration
	BatchSize              int
	ProcessingTimeout      time.Duration
	FastOperationTimeout   time.Duration
	MaxProcessingRetries   int
	RetryBackoff           time.Duration
	CleanupInterval        time.Duration
	CleanupRetention       time.Duration
	FailedRetryDelay       time.Duration
	MaxFailedRetries       int
	DoclingInitialInterval time.Duration
	DoclingMaxInterval     time.Duration
	DoclingBackoffFactor   float64
	DoclingMaxAttempts     int
	DoclingMaxLifetime     time.Duration
	DoclingRequeueDelay    time.Duration
	DoclingMaxRestarts     int
	UseSourceAsync         bool
	DefaultForceOCR        bool
	DefaultOCRLang         string
	DefaultPDFBackend      string
}

type Processor struct {
	queueWorker *QueueWorker
	maintenance *MaintenanceRunner
	metrics     *metrics.WorkerMetrics
	logger      *zap.Logger
}

func NewProcessor(repository repo.QueueRepository, conv domain.Converter, storage domain.ObjectStorage, logger *zap.Logger, cfg Config) *Processor {
	if logger == nil {
		logger = zap.NewNop()
	}

	normalized := normalizeConfig(cfg)
	workerMetrics := metrics.NewWorkerMetrics(logger)

	poller := NewDoclingPoller(conv, repository, workerMetrics, logger, DoclingPollerConfig{
		InitialInterval: normalized.DoclingInitialInterval,
		MaxInterval:     normalized.DoclingMaxInterval,
		BackoffFactor:   normalized.DoclingBackoffFactor,
		MaxAttempts:     normalized.DoclingMaxAttempts,
		RequeueDelay:    normalized.DoclingRequeueDelay,
		MaxRestarts:     normalized.DoclingMaxRestarts,
	})

	executor := NewConversionExecutor(repository, conv, storage, poller, workerMetrics, logger, ConversionExecutorConfig{
		MarkdownBucket:       normalized.MarkdownBucket,
		FastOperationTimeout: normalized.FastOperationTimeout,
		UseSourceAsync:       normalized.UseSourceAsync,
		DefaultForceOCR:      normalized.DefaultForceOCR,
		DefaultOCRLang:       normalized.DefaultOCRLang,
		DefaultPDFBackend:    normalized.DefaultPDFBackend,
	})

	queueWorker := NewQueueWorker(repository, executor, workerMetrics, logger, QueueWorkerConfig{
		Workers:      normalized.MaxWorkers,
		PollInterval: normalized.PollInterval,
		BatchSize:    normalized.BatchSize,
		RetryBackoff: normalized.RetryBackoff,
	})

	maintenance := NewMaintenanceRunner(repository, workerMetrics, logger, MaintenanceConfig{
		Interval:             normalized.CleanupInterval,
		ProcessingTimeout:    normalized.ProcessingTimeout,
		MaxProcessingRetries: normalized.MaxProcessingRetries,
		CleanupRetention:     normalized.CleanupRetention,
		DoclingMaxLifetime:   normalized.DoclingMaxLifetime,
		DoclingMaxAttempts:   normalized.DoclingMaxAttempts,
		FailedRetryDelay:     normalized.FailedRetryDelay,
		MaxFailedRetries:     normalized.MaxFailedRetries,
	})

	return &Processor{queueWorker: queueWorker, maintenance: maintenance, metrics: workerMetrics, logger: logger}
}

func (p *Processor) Run(ctx context.Context) {
	var wg sync.WaitGroup

	p.queueWorker.Start(ctx, &wg)
	p.maintenance.Start(ctx, &wg)

	<-ctx.Done()
	wg.Wait()
}

func normalizeConfig(cfg Config) Config {
	if cfg.MaxWorkers <= 0 {
		cfg.MaxWorkers = 1
	}
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = time.Second
	}
	if cfg.BatchSize <= 0 {
		cfg.BatchSize = 1
	}
	if cfg.RetryBackoff <= 0 {
		cfg.RetryBackoff = cfg.PollInterval
	}
	if cfg.CleanupInterval <= 0 {
		cfg.CleanupInterval = 30 * time.Second
	}
	if cfg.DoclingInitialInterval <= 0 {
		cfg.DoclingInitialInterval = 15 * time.Second
	}
	if cfg.DoclingBackoffFactor <= 1 {
		cfg.DoclingBackoffFactor = 2
	}
	if cfg.DoclingMaxInterval <= 0 {
		cfg.DoclingMaxInterval = 30 * time.Minute
	}
	if cfg.DoclingRequeueDelay <= 0 {
		cfg.DoclingRequeueDelay = cfg.PollInterval
	}
	if cfg.DoclingMaxRestarts <= 0 {
		cfg.DoclingMaxRestarts = 3
	}

	if cfg.DefaultPDFBackend == "" {
		cfg.DefaultPDFBackend = "pypdfium2"
	}

	return cfg
}
