package main

import (
	"context"
	"flag"
	"log"

	"backend-gateway-service/internal/config"
	"backend-gateway-service/internal/server"
)

func main() {
	configPath := flag.String("config", "config/config.yaml", "path to config file")
	flag.Parse()

	cfg, err := config.Load(*configPath)
	if err != nil {
		log.Fatalf("cannot load config: %v", err)
	}

	srv, err := server.New(cfg)
	if err != nil {
		log.Fatalf("cannot init server: %v", err)
	}

	if err := srv.Run(context.Background()); err != nil {
		log.Fatalf("server finished with error: %v", err)
	}
}
