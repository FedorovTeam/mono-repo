# Инфраструктурная схема

```mermaid
sequenceDiagram
    participant Client as gRPC клиент
    participant GRPC as DocumentService gRPC
    participant Service as DocumentService
    participant MinIO as MinIO (buckets)
    participant DB as PostgreSQL
    participant Temporal as Temporal Server (client)
    participant Worker as Temporal Worker
    participant Storage as storage-service (gRPC)
    participant Transformer as transformer-service (gRPC)
    participant Publisher as github-publisher-service (gRPC)
    participant GitHub as GitHub API
    participant Reconciler as UploadReconciler
    participant Retrier as WorkflowRetrier

    Client->>GRPC: Create/List/Get/UpdateStatus/ConfirmUpload
    GRPC->>Service: валидация, дефолты, маршрутизация
    Service-->>MinIO: проверка наличия bucket'ов и генерация ключей
    Service-->>DB: миграции и запись документов/версий/логов статусов
    Service->>Temporal: запуск DocumentProcessingWorkflow (ConfirmUpload)
    Worker-->>Temporal: поллинг task queue
    Worker-->>DB: чтение/обновление статусов и версий
    Worker->>Storage: GetObject (ожидание оригинала/markdown)
    Worker->>Transformer: ConvertDocumentToMarkdown
    Worker->>Storage: проверка наличия markdown
    Worker->>Publisher: PublishMarkdown (bucket + object key)
    Publisher->>GitHub: коммит/PR с markdown
    Reconciler-->>MinIO: StatObject для документов в статусе Uploaded
    Reconciler->>Service: ConfirmUpload при наличии объекта
    Retrier-->>DB: поиск попыток со start workflow errors
    Retrier->>Temporal: рестарт workflow для проблемных документов
```

- PostgreSQL хранит таблицы `documents`, `document_versions`, `document_status_logs`.
- MinIO используется для проверки существования bucket'ов, генерации ключей и статусов загрузки: файлы кладёт `storage-service`/`transformer-service`, а `UploadReconciler` проверяет наличие объектов напрямую через MinIO.
- Temporal Client в сервисе запускает workflow, Temporal Worker исполняет его и вызывает внешние gRPC‑сервисы; `WorkflowRetrier` перезапускает workflow при ошибках старта.
- PublishMarkdown выполняется через gRPC вызов `github-publisher-service`; прямой вызов GitHub API в `document-service` не используется.
- Корреляционный идентификатор передаётся через метаданные gRPC `x-correlation-id`, прокидывается в workflow и логирование.
