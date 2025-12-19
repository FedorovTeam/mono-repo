# Инфраструктурная схема

```mermaid
sequenceDiagram
    participant Client as gRPC клиент
    participant API as DocumentService (gRPC)
    participant DB as PostgreSQL (documents + processing_jobs)
    participant UploadReconciler as UploadReconciler (cron)
    participant Storage as storage-service (gRPC)
    participant ProcessingReconciler as ProcessingReconciler
    participant StoreWorker as StageWorker store_original
    participant ConvertWorker as StageWorker convert_to_markdown
    participant Transformer as docling-transformer-service (gRPC)
    participant PublishWorker as StageWorker publish_to_github
    participant Publisher as github-publisher-service (gRPC)
    participant JobFollower as JobFollower

    Client->>API: CreateDocument/ConfirmUpload/Get/List/UpdateStatus
    API->>DB: миграции + запись documents/document_versions/status_logs
    UploadReconciler-->>DB: выбирает Uploaded старше min_age
    UploadReconciler->>Storage: GetObject(bucket, object_key)
    UploadReconciler->>DB: ConfirmUpload ставит processing_jobs stage=store_original + job_events
    ProcessingReconciler-->>DB: возвращает застрявшие processing_jobs в pending
    StoreWorker-->>DB: берёт pending store_original
    StoreWorker->>DB: проставляет StoredOriginal и закрывает задачу
    JobFollower-->>DB: читает завершённые/failed задачи
    JobFollower->>DB: планирует convert_to_markdown или помечает Failed
    ConvertWorker-->>DB: берёт pending convert_to_markdown
    ConvertWorker->>Transformer: ConvertDocumentToMarkdown(bucket/object_key, wait=false)
    ConvertWorker-->>DB: переносит задачу при pending/waiting либо фиксирует ConvertedToMarkdown + markdown_key/queue_id
    JobFollower-->>DB: ставит publish_to_github после успешной конвертации
    PublishWorker-->>DB: берёт pending publish_to_github
    PublishWorker->>Publisher: PublishMarkdown(repo/branch/path, markdown_bucket/object_key, idempotency_key)
    PublishWorker-->>DB: фиксирует PublishedToGitHub/Failed и события (commit_sha/pr/retry_after)
```

- PostgreSQL хранит таблицы `documents`, `document_versions`, `document_status_logs`, очередь `processing_jobs` и аудит `job_events`.
- MinIO обслуживается `storage-service`: Document-service не генерирует URL для загрузки, а при `ConfirmUpload`/`UploadReconciler` лишь валидирует наличие объекта через `GetObject` по сохранённым `bucket`/`object_key`.
- Фоновые процессы состоят из `UploadReconciler` (ищет `Uploaded` и переиспользует ConfirmUpload), `ProcessingReconciler` (возвращает зависшие задачи), stage worker-ов `store_original`/`convert_to_markdown`/`publish_to_github` и `JobFollower`, который ставит следующую стадию или переводит документ в `Failed`. Очередь задач — `processing_jobs`, переходы статусов фиксируются в таблицах документов.
- Все фоновые задачи запускаются единым раннером на `errgroup.WithContext`: любая ошибка воркера мгновенно отменяет общий контекст и останавливает остальные задачи. gRPC‑сервер подписан на тот же контекст и завершается при сбое фонового контура.
- PublishMarkdown выполняется через gRPC вызов `github-publisher-service`; прямой вызов GitHub API в `document-service` не используется. Конвертация вызывается через `docling-transformer-service`, сохранение оригинала ограничивается сменой статуса, без прямого доступа к MinIO.
- Корреляционный идентификатор передаётся через метаданные gRPC `x-correlation-id` и прокидывается в цепочку cron-задач, вызовы конвертера/паблишера и логирование.
