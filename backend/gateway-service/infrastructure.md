# infrastructure

```mermaid
flowchart LR
    Client[GraphQL Client] -->|HTTPS :8080 + X-API-Key| Gateway[backend-gateway-service]

    subgraph Kubernetes
      Gateway -->|gRPC :9090| DocumentService
      Gateway -->|gRPC :9090| StorageService
      Gateway -->|OTLP gRPC :4317| OTelCollector[(OTLP Collector)]
    end

    DocumentService -->|PostgreSQL| DB[(Postgres)]
    StorageService -->|S3 API + presigned URL| MinIO[(MinIO)]
    MinIO -.->|HTTP webhook /internal/storage/uploaded| Gateway
```

Сервис принимает GraphQL-запросы на `:8080/query` с обязательным заголовком `X-API-Key`, отдаёт Playground по `:8080/playground` и прокидывает `x-correlation-id` во все gRPC-вызовы. Gateway обращается к `storage-service.GenerateUploadURL` (порт `9090`), чтобы получить `uploadUrl`, `bucket` и `objectKey` для прямой загрузки файла в MinIO; TTL пресайна берётся из `uploads.signedUrlTtlSeconds`. События MinIO с метаданными `x-amz-meta-correlation-id` отправляются на webhook `/internal/storage/uploaded`, после чего gateway подтверждает загрузку через `document-service.ConfirmUpload`.

