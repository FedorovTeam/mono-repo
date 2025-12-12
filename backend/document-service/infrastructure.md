# Инфраструктурная схема

```mermaid
sequenceDiagram
    participant Client as gRPC клиент
    participant GRPC as DocumentService gRPC
    participant Service as DocumentService
    participant MinIO as MinIO (buckets)
    participant DB as PostgreSQL
    participant UploadJob as Cron UploadReconciler
    participant ConvertJob as Cron MarkdownConverter
    participant PublishJob as Cron GithubPublisher
    participant Storage as storage-service (gRPC)
    participant Transformer as transformer-service (gRPC)
    participant Publisher as github-publisher-service (gRPC)
    participant GitHub as GitHub API

    Client->>GRPC: Create/List/Get/UpdateStatus/ConfirmUpload
    GRPC->>Service: валидация, дефолты, маршрутизация
    Service->>Storage: UploadOriginal/UploadMarkdown или GenerateUploadURL
    Service-->>DB: миграции и запись документов/версий/логов статусов
    UploadJob-->>DB: ищет документы в статусе Uploaded
    UploadJob->>Storage: GetObject для найденных ключей
    UploadJob->>Service: ConfirmUpload при наличии объекта
    ConvertJob-->>DB: выбирает StoredOriginal для конвертации
    ConvertJob->>Storage: GetObject для оригинала
    ConvertJob->>Transformer: ConvertDocumentToMarkdown
    ConvertJob-->>DB: сохраняет статус ConvertedToMarkdown и ключ markdown
    PublishJob-->>DB: выбирает ConvertedToMarkdown
    PublishJob->>Storage: проверяет наличие markdown
    PublishJob->>Publisher: PublishMarkdown (bucket + object key)
    Publisher->>GitHub: коммит/PR с markdown
    PublishJob-->>DB: фиксирует PublishedToGitHub или Failed
```

- PostgreSQL хранит таблицы `documents`, `document_versions`, `document_status_logs`.
- MinIO обслуживается `storage-service`: загрузка/выдача объектов выполняется через gRPC (`UploadOriginal`/`UploadMarkdown`/`GetObject`), а `UploadReconciler` использует этот API для проверки наличия исходников.
- Цепочка cron-воркеров (UploadReconciler → MarkdownConverter → GithubPublisher) читает статусы из PostgreSQL и двигает документ по шагам без Temporal.
- PublishMarkdown выполняется через gRPC вызов `github-publisher-service`; прямой вызов GitHub API в `document-service` не используется.
- Корреляционный идентификатор передаётся через метаданные gRPC `x-correlation-id`, прокидывается в цепочку cron-задач и логирование.
