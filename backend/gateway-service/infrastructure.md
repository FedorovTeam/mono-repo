# infrastructure

```mermaid
sequenceDiagram
    participant Client as GraphQL Client
    participant Gateway as gateway-service (HTTP :8080)
    participant Storage as storage-service (gRPC :9090)
    participant MinIO as MinIO (S3)
    participant Document as document-service (gRPC :9090)
    participant OTEL as OTLP Collector (gRPC :4317)

    Client->>Gateway: POST /query (X-API-Key, optional X-Correlation-ID)
    Gateway->>Gateway: CorrelationIDMiddleware + APIKeyMiddleware + CORS (console.fedorov-team.tech, localhost:3000)
    Gateway->>Storage: GenerateUploadURL (metadata x-correlation-id + x-api-key)
    Storage-->>Gateway: presigned PUT URL + upload/markdown buckets & keys
    Gateway->>Document: CreateDocument (upload/markdown buckets & keys, correlation-id; metadata x-correlation-id + x-api-key)
    Gateway-->>Client: uploadUrl + uploadMethod=PUT + документ

    Client->>MinIO: PUT файл по presigned URL (x-amz-meta-correlation-id)
    MinIO->>Gateway: POST /internal/storage/uploaded (MinIO notification payload)
    Gateway->>Gateway: Parse webhook, PathUnescape objectKey, documentId из basename, EnsureCorrelationID(userMetadata/responseElements)
    Gateway->>Document: ConfirmUpload(documentId, bucket, objectKey; metadata x-correlation-id)
    Document-->>Gateway: подтверждение статуса
    Gateway-->>MinIO: HTTP 202 при наличии обработанных записей, иначе 400

    Gateway-->>OTEL: OTLP gRPC трейсы/метрики при observability.enabled=true
```

Сервис поднимает HTTP GraphQL API на `:8080/query` (Playground — `/playground`). `APIKeyMiddleware` требует `X-API-Key` для всех методов, кроме preflight `OPTIONS`, а `CorrelationIDMiddleware` выставляет `X-Correlation-ID` в ответ и кладёт его в контекст; CORS разрешён только для `https://console.fedorov-team.tech` и `http://localhost:3000`.

`uploadKnowledgeDocument` запрашивает в `storage-service.GenerateUploadURL` пресайн с TTL из `uploads.signedUrlTtlSeconds`, передавая `x-correlation-id` и `x-api-key` в gRPC-метаданные; после успешного ответа `CreateDocument` в `document-service` получает те же correlation-id/api-key и ссылки на upload/markdown bucket/objectKey.

Webhook `/internal/storage/uploaded` не требует API-ключа: парсер MinIO извлекает `bucket/objectKey`, декодирует `objectKey`, достаёт `documentId` из basename и ищет `x-amz-meta-correlation-id` (case-insensitive) в `userMetadata/responseElements`; если идентификатор отсутствует, генерируется новый перед вызовом `document-service.ConfirmUpload`. Успешные уведомления завершаются `202 Accepted`, иначе возвращается `400`.

HTTP-обработчики GraphQL и вебхука обёрнуты `observability.HTTPHandler`: при `observability.enabled=true` трейсы и метрики отправляются в OTLP Collector по gRPC (`observability.otlpEndpoint`), при выключенной телеметрии используются no-op провайдеры без изменения бизнес-потока.
