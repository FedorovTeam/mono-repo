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
	"backend-document-service/internal/grpcdial"
	"backend-document-service/internal/model"
	"backend-document-service/internal/observability"
	"backend-document-service/internal/repository"
	docservice "backend-document-service/internal/service/document"
	"backend-document-service/internal/storage"
	transport "backend-document-service/internal/transport/grpc"
	githubpublisherv1 "backend-document-service/proto/backend/githubpublisher/v1"
	storagev1 "backend-document-service/proto/backend/storage/v1"
	transformerv1 "backend-document-service/proto/backend/transformer/v1"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
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

	minioClient, err := minio.New(cfg.Minio.Endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(cfg.Minio.AccessKey, cfg.Minio.SecretKey, ""),
		Secure: cfg.Minio.UseSSL,
	})
	if err != nil {
		log.Fatalf("minio init: %v", err)
	}

	repo := repository.New(db)
	keyManager := storage.NewMinioKeyManager(
		minioClient,
		cfg.Minio.BucketPrefix,
		cfg.Minio.OriginalBucket,
		cfg.Minio.MarkdownBucket,
	)

	storageConn, err := grpcdial.DialContext(ctx, cfg.Services.StorageService, cfg.Services.GRPC)
	if err != nil {
		log.Fatalf("storage client: %v", err)
	}
	defer storageConn.Close()

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

	storageClient := storagev1.NewStorageServiceClient(storageConn)
	transformerClient := transformerv1.NewTransformerServiceClient(transformerConn)
	publisherClient := githubpublisherv1.NewGithubPublisherServiceClient(publisherConn)
	activities := docservice.NewActivities(
		repo,
		storageClient,
		transformerClient,
		publisherClient,
		cfg.Github.Owner,
		cfg.Github.Repository,
		cfg.Github.Branch,
		cfg.Github.BaseBranch,
	)

	service := docservice.New(
		repo,
		keyManager,
		activities,
		cfg.Minio.OriginalBucket,
		cfg.Minio.MarkdownBucket,
		cfg.Github.Owner,
		cfg.Github.Repository,
		cfg.Github.Branch,
		cfg.Github.BaseBranch,
	)

	reconciler := docservice.NewUploadReconciler(
		repo,
		minioClient,
		service,
		cfg.Reconciler.BatchSize,
		cfg.Reconciler.MinUploadedAge,
	)

	go reconciler.Run(ctx, cfg.Reconciler.Interval)

	storeWorker := docservice.NewStageWorker(repo, activities, model.JobStageStoreOriginal, cfg.StageWorker.BatchSize, cfg.StageWorker.RetryDelay, false)
	convertWorker := docservice.NewStageWorker(repo, activities, model.JobStageConvertToMarkdown, cfg.StageWorker.BatchSize, cfg.StageWorker.RetryDelay, cfg.StageWorker.WaitMarkdown)
	publishWorker := docservice.NewStageWorker(repo, activities, model.JobStagePublishToGitHub, cfg.StageWorker.BatchSize, cfg.StageWorker.RetryDelay, false)

	go storeWorker.Run(ctx, cfg.StageWorker.Interval)
	go convertWorker.Run(ctx, cfg.StageWorker.Interval)
	go publishWorker.Run(ctx, cfg.StageWorker.Interval)

	githubRetrier := docservice.NewPublishRetrier(
		repo,
		activities,
		cfg.GithubRetryer.BatchSize,
		cfg.GithubRetryer.MaxAttempts,
		cfg.GithubRetryer.InitialBackoff,
		cfg.GithubRetryer.MaxBackoff,
	)

	go githubRetrier.Run(ctx, cfg.GithubRetryer.Interval)

	jobFollower := docservice.NewJobFollower(
		repo,
		cfg.JobFollower.BatchSize,
		cfg.JobFollower.RescheduleAfter,
	)

	go jobFollower.Run(ctx, cfg.JobFollower.Interval)

	server := transport.NewServer(service)
	if err := server.ListenAndServe(ctx, cfg.Server.Addr); err != nil {
		log.Printf("server stopped: %v", err)
		os.Exit(1)
	}
}
