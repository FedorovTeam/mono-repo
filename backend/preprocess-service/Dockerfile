FROM golang:1.23-alpine AS builder
WORKDIR /app
COPY . .
RUN go mod download
RUN CGO_ENABLED=0 go build -o app ./cmd/preprocess-service/main.go

FROM alpine:latest
WORKDIR /app
COPY --from=builder /app/app /app/app
CMD ["/app/app"]
