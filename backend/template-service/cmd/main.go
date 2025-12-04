package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"template-service/config"
	"template-service/internal/domain"
	"template-service/internal/infrastructure/cache"
	"template-service/internal/infrastructure/messaging"
	"template-service/internal/infrastructure/persistence"
	"template-service/internal/infrastructure/storage"
	grpcapi "template-service/internal/interfaces/grpc"
	httpapi "template-service/internal/interfaces/http"
	"template-service/internal/loop"
	"template-service/internal/usecase"
	"template-service/pkg/datebase"

	"github.com/gin-gonic/gin"
)

func main() {
	cfg := config.LoadConfig()
	if cfg == nil {
		log.Fatal("config is nil")
	}

	domain.SetRegionID(cfg.RegionID)

	// --- Подключение к YugabyteDB ---
	db, err := persistence.InitYugabyteDB(cfg.Yugabyte)
	if err != nil {
		log.Fatalf("failed to connect to YugabyteDB: %v", err)
	}
	defer db.Close()

	// --- Применение миграций через goose ---
	if err := datebase.ApplyMigrationsGoose(cfg.Yugabyte, "./db/migrations_ysql"); err != nil {
		log.Fatalf("Cannot apply goose migrations: %v", err)
	}

	// Создаем репозиторий через DI
	yugabyteRepo := persistence.NewDemoRepository(db)

	minioStorage, _ := storage.NewMinioFileStorage(cfg.Minio)
	natsPublisher, _ := messaging.NewNATSEventPublisher(cfg.Nats)
	natsSubscriber := natsPublisher // NATSEventPublisher реализует и подписку
	memoryCache := cache.NewMemoryCache()

	uc := usecase.NewDemoUseCase(
		yugabyteRepo,
		minioStorage,
		natsPublisher,
		natsSubscriber,
		memoryCache,
	)

	// Ensure JetStream streams for demo and DLQ subjects
	err = uc.EnsureStreamJetStream("DEMO", []string{cfg.Nats.DemoSubject})
	_ = uc.EnsureStreamJetStream("DLQ", []string{cfg.Nats.DemoDLQSubject})

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Запуск event loop через usecase
	go loop.StartEventLoop(uc, cfg.Nats)

	r := gin.Default()
	httpapi.RegisterRoutes(r, cfg, uc)
	httpapi.RegisterHealthz(r)

	srv := &http.Server{Addr: ":8080", Handler: r}
	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("failed to start HTTP server: %v", err)
		}
	}()

	go grpcapi.RunGRPCServer(ctx, uc, cfg.Service.GRPCPort)

	fmt.Println("MoiQR template-service started!")

	<-ctx.Done()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = srv.Shutdown(shutdownCtx)
}
