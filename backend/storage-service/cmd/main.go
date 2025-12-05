package main

import (
	"log"

	"backend-storage-service/config"
)

func main() {
	cfg := config.LoadConfig()
	if cfg == nil {
		log.Fatal("не удалось загрузить конфигурацию storage-service")
	}

	log.Printf("storage-service запущен на gRPC-порту %s, endpoint MinIO: %s", cfg.Service.GRPCPort, cfg.Minio.Endpoint)
	select {}
}
