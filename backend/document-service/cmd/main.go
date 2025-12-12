package main

import (
	"context"
	"database/sql"
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"backend-document-service/db/migrations"
	"backend-document-service/internal/config"
	"backend-document-service/internal/domain"
	"backend-document-service/internal/grpcdial"
	"backend-document-service/internal/infrastructure/persistence"
	storagestore "backend-document-service/internal/infrastructure/storage"
	transport "backend-document-service/internal/interfaces/grpc"
	"backend-document-service/internal/observability"
	docservice "backend-document-service/internal/service/document"
	docusecase "backend-document-service/internal/usecase/document"
	githubpublisherv1 "backend-document-service/proto/backend/githubpublisher/v1"
	storagev1 "backend-document-service/proto/backend/storage/v1"
	transformerv1 "backend-document-service/proto/backend/transformer/v1"

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

	repo := persistence.NewDocumentRepository(db)
	storageConn, err := grpcdial.DialContext(ctx, cfg.Services.StorageService, cfg.Services.GRPC)
	if err != nil {
		log.Fatalf("storage client: %v", err)
	}
	defer storageConn.Close()

	storageClient := storagev1.NewStorageServiceClient(storageConn)
	storageAdapter := storagestore.New(storageClient)
	transformerConn, err := grpcdial.DialContext(ctx, cfg.Services.TransformerService, cfg.Services.GRPC)
	if err != nil {
		log.Fatalf("transformer client: %v", err)
	}
	defer transformerConn.Close()

	publisherConn, err := grpcdial.DialContext(ctx, cfg.Services.GithubPublisherService, cfg.Services.GRPC)
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

	usecase := docusecase.New(repo, storageAdapter, defaults)

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

	go reconciler.Run(ctx, cfg.Reconciler.Interval)

	processingReconciler := docservice.NewProcessingReconciler(
		repo,
		cfg.StageWorker.BatchSize,
		cfg.StageWorker.Interval,
		cfg.StageWorker.GracePeriod,
	)

	go processingReconciler.Run(ctx, cfg.StageWorker.Interval)

	storeWorker := docservice.NewStageWorker(repo, activities, domain.JobStageStoreOriginal, cfg.StageWorker.BatchSize, cfg.StageWorker.RetryDelay)
	convertWorker := docservice.NewStageWorker(repo, activities, domain.JobStageConvertToMarkdown, cfg.StageWorker.BatchSize, cfg.StageWorker.RetryDelay)
	publishWorker := docservice.NewStageWorker(repo, activities, domain.JobStagePublishToGitHub, cfg.StageWorker.BatchSize, cfg.StageWorker.RetryDelay)

	go storeWorker.Run(ctx, cfg.StageWorker.Interval)
	go convertWorker.Run(ctx, cfg.StageWorker.Interval)
	go publishWorker.Run(ctx, cfg.StageWorker.Interval)

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
	)

	go jobFollower.Run(ctx, cfg.JobFollower.Interval)

	server := transport.NewServer(usecase)
	if err := server.ListenAndServe(ctx, cfg.Server.Addr); err != nil {
		log.Printf("server stopped: %v", err)
		os.Exit(1)
	}
}
