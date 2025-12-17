APP_NAME=preprocess-service
IMAGE?=$(APP_NAME):local
CONFIG_PATH?=$(PWD)/config/config.yaml
GOOSE?=go run github.com/pressly/goose/v3/cmd/goose@v3.22.3
MINIO_MC_IMAGE?=minio/mc:RELEASE.2024-06-11T03-13-30Z
GRPCURL_IMAGE?=fullstorydev/grpcurl:v1.9.1

.PHONY: build run test docker-build docker-run migrate demo

build:
	go build -o bin/$(APP_NAME) ./cmd/preprocess-service/main.go

run: build
	./bin/$(APP_NAME)

migrate:
	@if [ -z "$(POSTGRES_DSN)" ]; then \
		echo "POSTGRES_DSN is required for migrations"; \
		exit 1; \
	fi
	$(GOOSE) -dir migrations postgres "$(POSTGRES_DSN)" up

docker-build:
	docker build -t $(IMAGE) .

docker-run: docker-build
	docker run --rm \
	        -p 8082:8082 -p 9082:9082 \
	        -e CONFIG_PATH=/app/config/config.yaml \
	        -e POSTGRES_DSN=$(POSTGRES_DSN) \
	        -e MINIO_ENDPOINT=$(MINIO_ENDPOINT) \
	        -e MINIO_ACCESS_KEY=$(MINIO_ACCESS_KEY) \
	        -e MINIO_SECRET_KEY=$(MINIO_SECRET_KEY) \
	        -e MINIO_REGION=$(MINIO_REGION) \
	        -e MINIO_SSL=$(MINIO_SSL) \
	        -v $(CONFIG_PATH):/app/config/config.yaml:ro \
	        $(IMAGE)

test:
	go test ./...
