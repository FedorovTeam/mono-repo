# diagrams

```mermaid
flowchart LR
    Client -->|GraphQL + Authorization Bearer (+X-API-Key) + X-Correlation-ID| Gateway
    Gateway -->|GenerateUploadURL (gRPC)| StorageService
    Gateway -->|Create/Retry/List/Get (gRPC)| DocumentService
    Gateway -->|workflow trigger| DocumentService
    StorageService -->|presigned URL с x-amz-meta-correlation-id| MinIO
    DocumentService -->|ссылки/статусы| Postgres
```

- OTLP-коллектор показан отдельно: при `observability.enabled=false` сервис не создаёт экспортеры и работает с no-op провайдерами/хендлерами.
