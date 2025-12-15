# Инфраструктурная схема

```mermaid
sequenceDiagram
    participant Client as gRPC клиент
    participant GRPC as DocumentService gRPC
    participant Service as DocumentService
    participant DB as PostgreSQL
    participant UploadJob as Cron UploadReconciler
    participant ConvertJob as Cron MarkdownConverter
    participant PublishJob as Cron GithubPublisher
    participant Storage as storage-service (gRPC)
    participant Transformer as transformer-service (gRPC)
    participant Publisher as github-publisher-service (gRPC)

    Client->>GRPC: Create/List/Get/UpdateStatus/ConfirmUpload
    GRPC->>Service: валидация, дефолты, маршрутизация
    Service->>Storage: UploadOriginal/GenerateUploadURL (gRPC)
    Service-->>DB: миграции и запись документов/версий/логов статусов
    UploadJob-->>DB: ищет документы в статусе Uploaded
    UploadJob->>Storage: GetObject для найденных ключей
    UploadJob->>Service: ConfirmUpload при наличии объекта
    ConvertJob-->>DB: выбирает StoredOriginal для конвертации
    ConvertJob->>Transformer: ConvertDocumentToMarkdown (gRPC) с bucket/object_key
    ConvertJob-->>DB: сохраняет статус ConvertedToMarkdown и markdown_object_key/queue_id
    PublishJob-->>DB: выбирает ConvertedToMarkdown
    PublishJob->>Publisher: PublishMarkdown (gRPC) с markdown_bucket/object_key
    PublishJob-->>DB: фиксирует PublishedToGitHub или Failed
```

- PostgreSQL хранит таблицы `documents`, `document_versions`, `document_status_logs`.
- MinIO обслуживается `storage-service`: загрузка/выдача объектов выполняется через его gRPC API (`UploadOriginal`/`UploadMarkdown`/`GetObject`), а `UploadReconciler` использует этот API для проверки наличия исходников.
- Цепочка cron-воркеров (UploadReconciler → MarkdownConverter → GithubPublisher) читает статусы из PostgreSQL и двигает документ по шагам; Document-service не использует прямые SDK MinIO/GitHub/Docling.
- Все фоновые задачи запускаются единым раннером на `errgroup.WithContext`: любая ошибка воркера мгновенно отменяет общий контекст и останавливает остальные задачи. gRPC‑сервер подписан на тот же контекст и завершается при сбое фонового контура.
- PublishMarkdown выполняется через gRPC вызов `github-publisher-service`; прямой вызов GitHub API в `document-service` не используется.
- Корреляционный идентификатор передаётся через метаданные gRPC `x-correlation-id`, прокидывается в цепочку cron-задач и логирование.
