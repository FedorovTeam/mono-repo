# Базовый Dockerfile для шаблона MoiQR
FROM golang:1.21-alpine as builder
WORKDIR /app
COPY . .
RUN go mod download
RUN CGO_ENABLED=0 go build -o app ./cmd/main.go

FROM alpine:latest
WORKDIR /app
COPY --from=builder /app/app /app/
CMD ["/app/app"]
