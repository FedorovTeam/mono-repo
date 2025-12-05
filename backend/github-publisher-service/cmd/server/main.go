package main

import (
	"fmt"
	"log"
	"net"
	"net/http"
	"time"

	"backend-github-publisher-service/internal/config"
	"backend-github-publisher-service/internal/service"
	githubpublisherv1 "backend-github-publisher-service/proto/backend/githubpublisher/v1"
	"google.golang.org/grpc"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("failed to load config: %v", err)
	}

	addr := fmt.Sprintf(":%d", cfg.Service.GRPCPort)
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		log.Fatalf("failed to listen: %v", err)
	}

	grpcServer := grpc.NewServer()
	httpClient := &http.Client{Timeout: 15 * time.Second}
	publisher := service.NewPublisherServer(cfg, httpClient)

	githubpublisherv1.RegisterGithubPublisherServiceServer(grpcServer, publisher)
	log.Printf("github-publisher-service gRPC server listening on %s", addr)

	if err := grpcServer.Serve(listener); err != nil {
		log.Fatalf("gRPC server stopped: %v", err)
	}
}
