# Dockerfile

# Шаг 1: сборка бинаря
FROM golang:1.23.8-alpine AS builder

RUN apk update && apk add --no-cache git

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 GOOS=linux go build -o docling-transformer-service cmd/main.go

# Шаг 2: минимальный образ
FROM alpine:latest

WORKDIR /app

COPY --from=builder /app/docling-transformer-service .
COPY --from=builder /app/config/ ./config/
COPY --from=builder /app/db/ ./db/

CMD ["./docling-transformer-service"]
