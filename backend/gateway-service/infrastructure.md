# infrastructure

```mermaid
flowchart LR
    Client[GraphQL Client] -->|HTTPS :8080 /query + X-API-Key| Gateway[backend-gateway-service]
    Client -->|Presigned PUT HTTPS + x-amz-meta-correlation-id| MinIO[(MinIO)]

    subgraph Kubernetes
      Gateway -->|gRPC :9090\n+x-api-key\n+x-correlation-id| DocumentService
      Gateway -->|gRPC :9090\n+x-api-key\n+x-correlation-id| StorageService
      Gateway -->|OTLP gRPC :4317| OTelCollector[(OTLP Collector)]
    end

    DocumentService -->|PostgreSQL| DB[(Postgres)]
    StorageService -->|Issues presigned URL\n(bucket, objectKey, TTL)| MinIO
    MinIO -.->|HTTP webhook\n/internal/storage/uploaded\n+corr. id from metadata| Gateway
    Gateway -->|ConfirmUpload (gRPC)\n+correlation-id| DocumentService
```

Сервис поднимает GraphQL API на `:8080/query` (Playground — `/playground`) с обязательным заголовком `X-API-Key` и CORS-доступом только для `https://console.fedorov-team.tech` и `http://localhost:3000`. Каждый запрос получает `X-Correlation-ID`, который пробрасывается в gRPC-метаданные `x-correlation-id` и `x-api-key` при обращениях к `document-service` и `storage-service`.

Мутация `uploadKnowledgeDocument` запрашивает в `storage-service.GenerateUploadURL` пресайн с TTL из `uploads.signedUrlTtlSeconds`; клиент загружает файл напрямую в MinIO по выданному PUT URL, добавляя `x-amz-meta-correlation-id` для последующей трассировки. После события загрузки MinIO отправляет webhook на `/internal/storage/uploaded`, gateway извлекает `documentId` из object key и подтверждает загрузку через `document-service.ConfirmUpload`.

Телеметрия (трейсы и метрики) уходит по OTLP gRPC (`observability.enabled=true` и `observability.otlpEndpoint`). При выключенной телеметрии используются no-op провайдеры, но сервис продолжает работать с проверкой API-ключа и прокладкой корреляционных идентификаторов.
