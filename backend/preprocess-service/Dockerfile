# Шаг 1: сборка бинаря
FROM golang:1.24-bookworm AS builder

WORKDIR /app

# Иногда нужен git/ca-certificates для приватных модулей и https
RUN apt-get update \
    && apt-get install -y --no-install-recommends git ca-certificates \
    && rm -rf /var/lib/apt/lists/*

COPY go.mod go.sum ./
RUN go mod download

COPY . .

# Собираем ПАКЕТ директории cmd, а не main.go файлом
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o preprocess-service cmd/main.go

# Шаг 2: рантайм образ
FROM debian:bookworm-slim

RUN apt-get update \
    && apt-get install -y --no-install-recommends \
        libreoffice \
        fontconfig \
        ca-certificates \
        curl \
    && apt-get clean \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /app

COPY --from=builder /app/preprocess-service ./preprocess-service
COPY --from=builder /app/config/ ./config/
COPY --from=builder /app/db/ ./db/


RUN mkdir -p /tmp/preprocess-service/work /tmp/preprocess-service/output \
    && chmod 0777 /tmp/preprocess-service/work /tmp/preprocess-service/output

ENV CONFIG_PATH=/app/config/config.yaml

CMD ["./preprocess-service"]
