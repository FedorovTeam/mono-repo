package main

import (
	"backend-github-publisher-service/internal/infrastructure/storage"
	"backend-github-publisher-service/pkg/config"
	"backend-github-publisher-service/pkg/observability"
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"backend-github-publisher-service/internal/service"
	"backend-github-publisher-service/internal/worker"
	githubpublisherv1 "backend-github-publisher-service/proto/backend/githubpublisher/v1"
	"google.golang.org/grpc"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("failed to load config: %v", err)
	}

	otelProviders, shutdownTelemetry, err := observability.Setup(context.Background(), cfg.Observability)
	if err != nil {
		log.Fatalf("failed to init observability: %v", err)
	}
	defer func() {
		if err := shutdownTelemetry(context.Background()); err != nil {
			log.Printf("failed to shutdown observability: %v", err)
		}
	}()

	addr := fmt.Sprintf(":%d", cfg.Service.GRPCPort)
	minioStorage, err := storage.NewMinioMarkdownLoader(cfg.Minio)
	if err != nil {
		log.Fatalf("failed to init minio client: %v", err)
	}

	workerCfg := worker.Config{
		DSN:          cfg.Processing.DSN,
		Stage:        cfg.Processing.Stage,
		PollInterval: cfg.Processing.PollInterval,
		MaxAttempts:  cfg.Processing.MaxAttempts,
		Backoff:      cfg.Processing.Backoff,
	}

	processingWorker, err := worker.New(ctx, workerCfg)
	if err != nil {
		log.Fatalf("failed to init processing worker: %v", err)
	}
	defer processingWorker.Close()

	listener, err := net.Listen("tcp", addr)
	if err != nil {
		log.Fatalf("failed to listen: %v", err)
	}

	grpcServer := grpc.NewServer(observability.GRPCServerOptions(otelProviders)...)
	httpClient := &http.Client{Timeout: 15 * time.Second, Transport: observability.HTTPTransport(otelProviders, nil)}
	publisher := service.NewPublisherServer(cfg, httpClient, minioStorage)

	githubpublisherv1.RegisterGithubPublisherServiceServer(grpcServer, publisher)
	go func() {
		log.Printf("starting processing worker for stage=%s", workerCfg.Stage)
		processingWorker.Run(ctx)
	}()

	go func() {
		<-ctx.Done()
		grpcServer.GracefulStop()
	}()

	log.Printf("github-publisher-service gRPC server listening on %s", addr)

	if err := grpcServer.Serve(listener); err != nil && !errors.Is(err, grpc.ErrServerStopped) {
		log.Fatalf("gRPC server stopped: %v", err)
	}
}
