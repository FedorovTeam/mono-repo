package main

import (
	"backend-document-service/internal/infrastructure/grpcclient"
	"backend-document-service/internal/infrastructure/postgres"
	"backend-document-service/pkg/config"
	"backend-document-service/pkg/observability"
	"context"
	"database/sql"
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"backend-document-service/db/migrations"
	"backend-document-service/internal/domain"
	storagestore "backend-document-service/internal/infrastructure/storage"
	transport "backend-document-service/internal/interfaces/grpc"
	docservice "backend-document-service/internal/service/document"
	docusecase "backend-document-service/internal/usecase/document"
	githubpublisherv1 "backend-document-service/proto/backend/githubpublisher/v1"
	storagev1 "backend-document-service/proto/backend/storage/v1"
	transformerv1 "backend-document-service/proto/backend/transformer/v1"
	"golang.org/x/sync/errgroup"

	_ "github.com/jackc/pgx/v5/stdlib"
)

func main() {
	configPath := flag.String("config", "config/config.yaml", "path to config file")
	migrateOnly := flag.Bool("migrate-only", false, "run migrations and exit")
	flag.Parse()

	cfg, err := config.Load(*configPath)
	if err != nil || cfg == nil {
		log.Fatalf("load config: %v", err)
	}

	stopLogging := observability.SetupLogging(cfg.Observability.Logs.Enabled)
	defer stopLogging()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	shutdownObservability, err := observability.SetupOpenTelemetry(ctx, "document-service", cfg.Observability)
	if err != nil {
		log.Fatalf("init observability: %v", err)
	}
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := shutdownObservability(shutdownCtx); err != nil {
			log.Printf("shutdown observability: %v", err)
		}
	}()

	db, err := sql.Open("pgx", cfg.Postgres.DSN)
	if err != nil {
		log.Fatalf("connect db: %v", err)
	}
	defer db.Close()
	db.SetConnMaxLifetime(5 * time.Minute)

	// 🔹 ВСЕГДА прогоняем миграции при старте
	if err := migrations.Run(ctx, db); err != nil {
		log.Fatalf("run migrations: %v", err)
	}

	// Если сервис запущен в режиме "только миграции" — выходим
	if *migrateOnly {
		log.Println("migrations applied, exiting because of -migrate-only")
		return
	}

	repo := postgres.New(db)
	storageConn, err := grpcclient.NewGRPCClient(
		cfg.Services.StorageService,
		cfg.ServicesGRPC,
	)
	if err != nil {
		log.Fatalf("storage client: %v", err)
	}
	defer storageConn.Close()

	storageClient := storagev1.NewStorageServiceClient(storageConn)
	storageAdapter := storagestore.New(storageClient)

	transformerConn, err := grpcclient.NewGRPCClient(
		cfg.Services.TransformerService,
		cfg.ServicesGRPC,
	)
	if err != nil {
		log.Fatalf("transformer client: %v", err)
	}
	defer transformerConn.Close()

	publisherConn, err := grpcclient.NewGRPCClient(
		cfg.Services.GithubPublisherService,
		cfg.ServicesGRPC,
	)
	if err != nil {
		log.Fatalf("publisher client: %v", err)
	}
	defer publisherConn.Close()

	transformerClient := transformerv1.NewTransformerServiceClient(transformerConn)
	publisherClient := githubpublisherv1.NewGithubPublisherServiceClient(publisherConn)

	defaults := docusecase.Defaults{
		OriginalBucket: cfg.Storage.OriginalBucket,
		MarkdownBucket: cfg.Storage.MarkdownBucket,
		Owner:          cfg.Github.Owner,
		Repository:     cfg.Github.Repository,
		Branch:         cfg.Github.Branch,
		BaseBranch:     cfg.Github.BaseBranch,
	}

	router := newStageRouter(cfg.Router)

	usecase := docusecase.New(repo, storageAdapter, defaults, router)

	activities := docservice.NewActivities(
		repo,
		transformerClient,
		publisherClient,
	)

	reconciler := docservice.NewUploadReconciler(
		repo,
		usecase,
		cfg.Reconciler.BatchSize,
		cfg.Reconciler.MinUploadedAge,
	)

	processingReconciler := docservice.NewProcessingReconciler(
		repo,
		cfg.StageWorker.BatchSize,
		cfg.StageWorker.Interval,
		cfg.StageWorker.GracePeriod,
	)

	storeWorker := docservice.NewStageWorker(repo, activities, domain.JobStageStoreOriginal, cfg.StageWorker.BatchSize, cfg.StageWorker.RetryDelay)
	convertWorker := docservice.NewStageWorker(repo, activities, domain.JobStageConvertToMarkdown, cfg.StageWorker.ConvertBatchSize, cfg.StageWorker.RetryDelay)
	publishWorker := docservice.NewStageWorker(repo, activities, domain.JobStagePublishToGitHub, cfg.StageWorker.BatchSize, cfg.StageWorker.RetryDelay)

	publishDefaults := docservice.PublishDefaults{
		CommitMessage: cfg.Github.CommitMessage,
		PRTitle:       cfg.Github.PRTitle,
		PRBody:        cfg.Github.PRBody,
		AuthorName:    cfg.Github.AuthorName,
		AuthorEmail:   cfg.Github.AuthorEmail,
	}

	jobFollower := docservice.NewJobFollower(
		repo,
		cfg.JobFollower.BatchSize,
		cfg.JobFollower.RescheduleAfter,
		publishDefaults,
		router,
	)

	workers := []docservice.PeriodicWorker{
		{Name: "upload-reconciler", Interval: cfg.Reconciler.Interval, Run: reconciler.Run},
		{Name: "processing-reconciler", Interval: cfg.StageWorker.Interval, Run: processingReconciler.Run},
		{Name: "stage-store", Interval: cfg.StageWorker.Interval, Run: storeWorker.Run},
		{Name: "stage-convert", Interval: cfg.StageWorker.Interval, Run: convertWorker.Run},
		{Name: "stage-publish", Interval: cfg.StageWorker.Interval, Run: publishWorker.Run},
		{Name: "job-follower", Interval: cfg.JobFollower.Interval, Run: jobFollower.Run},
	}

	server := transport.NewServer(usecase)

	group, ctx := errgroup.WithContext(ctx)

	group.Go(func() error {
		return docservice.RunBackgroundWorkers(ctx, workers...)
	})

	group.Go(func() error {
		return server.ListenAndServe(ctx, cfg.Server.Addr)
	})

	if err := group.Wait(); err != nil {
		log.Printf("service stopped: %v", err)
		os.Exit(1)
	}
}

func newStageRouter(cfg config.Router) *domain.StageRouter {
	rules := domain.RouterRules{
		PreprocessEnabled:    cfg.PreprocessEnabled,
		PreprocessExtensions: cfg.PreprocessExtensions,
	}
	for _, rule := range cfg.RepositoryRules {
		rules.RepositoryRules = append(rules.RepositoryRules, domain.RepositoryRouterRule{
			Owner:                rule.Owner,
			Repository:           rule.Repository,
			PreprocessEnabled:    rule.PreprocessEnabled,
			PreprocessExtensions: rule.PreprocessExtensions,
		})
	}
	return domain.NewStageRouter(rules)
}
