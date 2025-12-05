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
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/worker"
	"go.temporal.io/sdk/workflow"
	"google.golang.org/grpc"
)

func main() {
	cfgPath := flag.String("config", "config.yaml", "path to config file")
	migrate := flag.Bool("migrate", false, "run migrations and exit")
	flag.Parse()

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		log.Fatalf("load config: %v", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	db, err := sql.Open("pgx", cfg.Postgres.DSN)
	if err != nil {
		log.Fatalf("connect db: %v", err)
	}
	defer db.Close()
	db.SetConnMaxLifetime(time.Minute * 5)

	if *migrate {
		if err := migrations.Run(ctx, db); err != nil {
			log.Fatalf("run migrations: %v", err)
		}
		log.Println("migrations applied")
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
	keyManager := storage.NewMinioKeyManager(minioClient, cfg.Minio.BucketPrefix, cfg.Minio.OriginalBucket, cfg.Minio.MarkdownBucket)

	propagator := docservice.NewCorrelationIDPropagator()
	temporalClient, err := client.Dial(client.Options{HostPort: cfg.Temporal.Address, Namespace: cfg.Temporal.Namespace, ContextPropagators: []workflow.ContextPropagator{propagator}})
	if err != nil {
		log.Fatalf("temporal client: %v", err)
	}
	defer temporalClient.Close()

	storageConn, err := grpc.DialContext(ctx, cfg.Services.StorageService, grpc.WithInsecure())
	if err != nil {
		log.Fatalf("storage client: %v", err)
	}
	defer storageConn.Close()

	transformerConn, err := grpc.DialContext(ctx, cfg.Services.TransformerService, grpc.WithInsecure())
	if err != nil {
		log.Fatalf("transformer client: %v", err)
	}
	defer transformerConn.Close()

	publisherConn, err := grpc.DialContext(ctx, cfg.Services.GithubPublisherService, grpc.WithInsecure())
	if err != nil {
		log.Fatalf("publisher client: %v", err)
	}
	defer publisherConn.Close()

	storageClient := storagev1.NewStorageServiceClient(storageConn)
	transformerClient := transformerv1.NewTransformerServiceClient(transformerConn)
	publisherClient := githubpublisherv1.NewGithubPublisherServiceClient(publisherConn)

	workerInstance := worker.New(temporalClient, cfg.Temporal.TaskQueue, worker.Options{})
	activities := docservice.NewActivities(repo, storageClient, transformerClient, publisherClient, cfg.Github.Repository, cfg.Github.Branch)
	workerInstance.RegisterWorkflow(docservice.DocumentProcessingWorkflow)
	workerInstance.RegisterActivityWithOptions(activities.UploadOriginal, activity.RegisterOptions{Name: docservice.ActivityUploadOriginalName})
	workerInstance.RegisterActivityWithOptions(activities.ConvertToMarkdown, activity.RegisterOptions{Name: docservice.ActivityConvertToMarkdownName})
	workerInstance.RegisterActivityWithOptions(activities.PublishMarkdown, activity.RegisterOptions{Name: docservice.ActivityPublishMarkdownName})

	go func() {
		if err := workerInstance.Run(worker.InterruptCh()); err != nil {
			log.Fatalf("temporal worker: %v", err)
		}
	}()

	service := docservice.New(repo, keyManager, temporalClient, cfg.Temporal.TaskQueue, activities)

	server := transport.NewServer(service)
	if err := server.ListenAndServe(ctx, cfg.Server.Addr); err != nil {
		log.Printf("server stopped: %v", err)
		os.Exit(1)
	}
}
