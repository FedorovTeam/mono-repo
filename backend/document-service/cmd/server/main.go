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
	"backend-document-service/internal/repository"
	docservice "backend-document-service/internal/service/document"
	"backend-document-service/internal/storage"
	transport "backend-document-service/internal/transport/grpc"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

func main() {
	addr := flag.String("addr", ":50051", "gRPC listen address")
	dsn := flag.String("dsn", "postgres://postgres:postgres@localhost:5432/document?sslmode=disable", "PostgreSQL DSN")
	migrate := flag.Bool("migrate", false, "run migrations and exit")
	minioEndpoint := flag.String("minio-endpoint", "localhost:9000", "MinIO endpoint")
	minioAccessKey := flag.String("minio-access", "minioadmin", "MinIO access key")
	minioSecretKey := flag.String("minio-secret", "minioadmin", "MinIO secret key")
	minioUseSSL := flag.Bool("minio-ssl", false, "Use SSL for MinIO")
	originalBucket := flag.String("original-bucket", "backend-original", "Bucket for original files")
	markdownBucket := flag.String("markdown-bucket", "backend-md", "Bucket for markdown files")
	flag.Parse()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	db, err := sql.Open("pgx", *dsn)
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

	minioClient, err := minio.New(*minioEndpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(*minioAccessKey, *minioSecretKey, ""),
		Secure: *minioUseSSL,
	})
	if err != nil {
		log.Fatalf("minio init: %v", err)
	}

	repo := repository.New(db)
	keyManager := storage.NewMinioKeyManager(minioClient, "documents", *originalBucket, *markdownBucket)
	service := docservice.New(repo, keyManager)

	server := transport.NewServer(service)
	if err := server.ListenAndServe(ctx, *addr); err != nil {
		log.Printf("server stopped: %v", err)
		os.Exit(1)
	}
}
