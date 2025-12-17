FROM golang:1.24-bookworm AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o app ./cmd/preprocess-service/main.go

FROM debian:bookworm-slim
RUN apt-get update \
    && apt-get install -y --no-install-recommends libreoffice fontconfig ca-certificates curl \
    && apt-get clean \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /app
COPY --from=builder /app/app /app/app
COPY config/config.yaml /app/config/config.yaml

RUN mkdir -p /tmp/preprocess-service/work /tmp/preprocess-service/output \
    && chmod 0777 /tmp/preprocess-service/work /tmp/preprocess-service/output

ENV CONFIG_PATH=/app/config/config.yaml
CMD ["/app/app"]
