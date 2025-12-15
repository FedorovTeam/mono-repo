package main

import (
	"context"
	"flag"
	"log"

	"backend-gateway-service/internal/app"
)

func main() {
	configPath := flag.String("config", "config/config.yaml", "path to config file")
	flag.Parse()

	application, err := app.New(*configPath)
	if err != nil {
		log.Fatalf("cannot init application: %v", err)
	}

	if err := application.Run(context.Background()); err != nil {
		log.Fatalf("application finished with error: %v", err)
	}
}
