package main

import (
	"context"
	"log"
	"net"
	"os"
	"os/signal"
	"syscall"

	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"

	"backend-storage-service/config"
	"backend-storage-service/internal/observability"
	"backend-storage-service/internal/storage"
	"backend-storage-service/internal/transport/grpcserver"
	storagev1 "backend-storage-service/proto/backend/storage/v1"
)

func main() {
	cfg := config.LoadConfig()
	if cfg == nil {
		log.Fatal("не удалось загрузить конфигурацию storage-service")
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	uploadBucket := cfg.Events.UploadBucket
	if uploadBucket == "" {
		uploadBucket = cfg.Minio.Buckets.KnowledgeDocx
	}

	buckets := []string{
		cfg.Minio.Buckets.KnowledgeDocx,
		cfg.Minio.Buckets.KnowledgeMD,
	}
	if uploadBucket != "" && uploadBucket != cfg.Minio.Buckets.KnowledgeDocx && uploadBucket != cfg.Minio.Buckets.KnowledgeMD {
		buckets = append(buckets, uploadBucket)
	}

	minioAPI, err := storage.NewMinioClient(ctx, storage.MinioClientConfig{
		Endpoint:  cfg.Minio.Endpoint,
		AccessKey: cfg.Minio.AccessKey,
		SecretKey: cfg.Minio.SecretKey,
		UseSSL:    cfg.Minio.UseSSL,
		Buckets:   buckets,
	})
	if err != nil {
		log.Fatalf("не удалось инициализировать MinIO клиент: %v", err)
	}

	if err := storage.ConfigureUploadNotification(ctx, minioAPI, uploadBucket, cfg.Events.WebhookArn); err != nil {
		log.Fatalf("не удалось настроить webhook уведомления: %v", err)
	}

	workerPool, err := startProcessingWorker(ctx, cfg.Processing)
	if err != nil {
		log.Fatalf("не удалось запустить воркер обработки задач: %v", err)
	}
	defer workerPool.Close()

	keyGenerator := storage.NewObjectKeyGenerator(nil)
	storageService := storage.NewService(
		minioAPI,
		keyGenerator,
		cfg.Minio.Buckets.KnowledgeDocx,
		cfg.Minio.Buckets.KnowledgeMD,
	)

	log.Printf("инициализированы бакеты: docx=%s, md=%s", cfg.Minio.Buckets.KnowledgeDocx, cfg.Minio.Buckets.KnowledgeMD)

	observabilityCfg := observability.Config{
		EnableMetrics: cfg.Observability.EnableMetrics,
		EnableTraces:  cfg.Observability.EnableTraces,
		EnableLogs:    cfg.Observability.EnableLogs,
	}

	observability.ConfigureProviders(observabilityCfg)
	serverOptions := observability.GRPCServerOptions(observabilityCfg, correlationLoggingInterceptor())
	grpcServer := grpc.NewServer(serverOptions...)
	storagev1.RegisterStorageServiceServer(grpcServer, grpcserver.New(minioAPI, keyGenerator, cfg.Minio.Buckets.KnowledgeDocx, cfg.Minio.Buckets.KnowledgeMD, storageService))

	listener, err := net.Listen("tcp", cfg.Service.GRPCPort)
	if err != nil {
		log.Fatalf("не удалось открыть порт %s: %v", cfg.Service.GRPCPort, err)
	}

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
