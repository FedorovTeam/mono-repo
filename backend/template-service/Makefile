APP_NAME=template-service

.PHONY: build run test lint migrate proto

build:
	go build -o bin/$(APP_NAME) ./cmd/main.go

run:
	./bin/$(APP_NAME)

test:
	go test ./...

lint:
	golangci-lint run

migrate:
	goose -dir db/migrations_ysql postgres "$$DB_CONN" up

proto:
	protoc --go_out=. --go-grpc_out=. proto/*.proto
