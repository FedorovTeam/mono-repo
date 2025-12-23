package main

import (
	"backend-github-publisher-service/internal/infrastructure/github"
	"backend-github-publisher-service/internal/infrastructure/storage"
	"backend-github-publisher-service/pkg/config"
	"backend-github-publisher-service/pkg/logger"
	"backend-github-publisher-service/pkg/observability"
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"backend-github-publisher-service/internal/service"
	githubpublisherv1 "backend-github-publisher-service/proto/backend/githubpublisher/v1"
	"google.golang.org/grpc"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to load config: %v\n", err)
		os.Exit(1)
	}

	baseLogger, zapSugar, syncLogger, err := logger.Init(cfg.Logging)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to init logger: %v\n", err)
		os.Exit(1)
	}
	defer func() {
		if err := syncLogger(); err != nil {
			fmt.Fprintf(os.Stderr, "failed to sync logger: %v\n", err)
		}
	}()

	otelProviders, shutdownTelemetry, err := observability.Setup(context.Background(), cfg.Observability)
	if err != nil {
		zapSugar.Fatalf("failed to init observability: %v", err)
	}
	defer func() {
		if err := shutdownTelemetry(context.Background()); err != nil {
			zapSugar.Warnf("failed to shutdown observability: %v", err)
		}
	}()

	addr := fmt.Sprintf(":%d", cfg.Service.GRPCPort)
	minioStorage, err := storage.NewMinioMarkdownLoader(cfg.Minio)
	if err != nil {
		zapSugar.Fatalf("failed to init minio client: %v", err)
	}

	listener, err := net.Listen("tcp", addr)
	if err != nil {
		zapSugar.Fatalf("failed to listen: %v", err)
	}

	grpcServer := grpc.NewServer(observability.GRPCServerOptions(otelProviders)...)
	httpClient := &http.Client{Timeout: 15 * time.Second, Transport: observability.HTTPTransport(otelProviders, nil)}
	publisherAdapter := github.NewPublisher(cfg.GitHub, httpClient)
	publisher := service.NewPublisherServer(cfg, baseLogger, publisherAdapter, minioStorage)

	githubpublisherv1.RegisterGithubPublisherServiceServer(grpcServer, publisher)
	go func() {
		<-ctx.Done()
		grpcServer.GracefulStop()
	}()

	zapSugar.Infof("github-publisher-service gRPC server listening on %s", addr)

	if err := grpcServer.Serve(listener); err != nil && !errors.Is(err, grpc.ErrServerStopped) {
		zapSugar.Fatalf("gRPC server stopped: %v", err)
	}
}
