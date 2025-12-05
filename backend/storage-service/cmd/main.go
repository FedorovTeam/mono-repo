package main

import (
	"context"
	"log"
	"net"
	"os"
	"os/signal"
	"syscall"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"

	"backend-storage-service/config"
	"backend-storage-service/internal/storage"
	"backend-storage-service/internal/transport/grpcserver"
	storagev1 "backend-storage-service/proto/backend/storage/v1"
)

func main() {
	cfg := config.LoadConfig()
	if cfg == nil {
		log.Fatal("не удалось загрузить конфигурацию storage-service")
	}

	minioClient, err := minio.New(cfg.Minio.Endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(cfg.Minio.AccessKey, cfg.Minio.SecretKey, ""),
		Secure: cfg.Minio.UseSSL,
	})
	if err != nil {
		log.Fatalf("не удалось инициализировать MinIO клиент: %v", err)
	}

	minioAPI := storage.NewMinioAPI(minioClient)
	keyGenerator := storage.NewObjectKeyGenerator(nil)
	storageService := storage.NewService(
		minioAPI,
		keyGenerator,
		cfg.Minio.Buckets.KnowledgeDocx,
		cfg.Minio.Buckets.KnowledgeMD,
	)

	log.Printf("инициализированы бакеты: docx=%s, md=%s", cfg.Minio.Buckets.KnowledgeDocx, cfg.Minio.Buckets.KnowledgeMD)

	grpcServer := grpc.NewServer(grpc.UnaryInterceptor(correlationLoggingInterceptor()))
	storagev1.RegisterStorageServiceServer(grpcServer, grpcserver.New(minioAPI, keyGenerator, cfg.Minio.Buckets.KnowledgeDocx, storageService))

	listener, err := net.Listen("tcp", cfg.Service.GRPCPort)
	if err != nil {
		log.Fatalf("не удалось открыть порт %s: %v", cfg.Service.GRPCPort, err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	go func() {
		<-ctx.Done()
		log.Println("получен сигнал завершения, останавливаем gRPC сервер")
		grpcServer.GracefulStop()
	}()

	log.Printf("storage-service запущен на gRPC-порту %s, endpoint MinIO: %s", cfg.Service.GRPCPort, cfg.Minio.Endpoint)

	if err := grpcServer.Serve(listener); err != nil && ctx.Err() == nil {
		log.Fatalf("gRPC сервер завершился с ошибкой: %v", err)
	}

	_ = storageService
}

func correlationLoggingInterceptor() grpc.UnaryServerInterceptor {
	logger := log.Default()

	return func(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
		correlationID := correlationIDFromContext(ctx, req)
		logger.Printf("gRPC request started: method=%s correlation_id=%s", info.FullMethod, correlationID)

		resp, err := handler(ctx, req)
		if err != nil {
			logger.Printf("gRPC request failed: method=%s correlation_id=%s error=%v", info.FullMethod, correlationID, err)
			return resp, err
		}

		logger.Printf("gRPC request completed: method=%s correlation_id=%s", info.FullMethod, correlationID)
		return resp, nil
	}
}

func correlationIDFromContext(ctx context.Context, req interface{}) string {
	type correlationGetter interface {
		GetCorrelationId() string
	}

	if getter, ok := req.(correlationGetter); ok {
		if id := getter.GetCorrelationId(); id != "" {
			return id
		}
	}

	if md, ok := metadata.FromIncomingContext(ctx); ok {
		if values := md.Get("x-correlation-id"); len(values) > 0 && values[0] != "" {
			return values[0]
		}
	}

	return ""
}
