APP_NAME=preprocess-service

.PHONY: build run test

build:
	go build -o bin/$(APP_NAME) ./cmd/preprocess-service/main.go

run: build
	./bin/$(APP_NAME)

test:
	go test ./...
