# Dockerfile

# Шаг 1: сборка бинаря
FROM golang:1.25-alpine AS builder

RUN apk update && apk add --no-cache git

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 GOOS=linux go build -o storage-service ./cmd

# Шаг 2: минимальный образ
FROM alpine:latest

WORKDIR /app

COPY --from=builder /app/storage-service .
COPY --from=builder /app/config/ ./config/

CMD ["./storage-service"]
