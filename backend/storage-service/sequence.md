# Sequence diagram

```mermaid
sequenceDiagram
    participant Client as Клиент / Gateway
    participant Storage as StorageService (gRPC + worker)
    participant Postgres as PostgreSQL
    participant MinIO

    Note over Storage,MinIO: Старт: загрузка config.yaml, выбор uploadBucket (events.uploadBucket или knowledge-original)
    Storage->>MinIO: NewMinioClient + initializeBuckets(docx, md, uploadBucket*)
    MinIO-->>Storage: OK
    Note over Storage,MinIO: ConfigureUploadNotification(uploadBucket, events.webhookArn) если задано
    Storage->>MinIO: SetBucketNotification(ObjectCreatedAll → targetArn)
    MinIO-->>Storage: OK
    Note over Storage,Postgres: Запуск in-process воркера: подключение processing.dsn, stage=cfg.stage
    Storage->>Postgres: pgxpool.New(processing.dsn)
    loop каждые PollInterval
        Storage->>Postgres: SELECT pending processing_jobs WHERE stage=cfg.stage FOR UPDATE SKIP LOCKED
        alt Задача найдена
            Storage->>Postgres: UPDATE processing_jobs SET status='in_progress'
            Storage->>Postgres: UPDATE documents SET status='StoredOriginal', actual_status='StoredOriginal', error_reason=''
            Storage->>Postgres: INSERT document_status_logs(workflow_id из payload.correlation_id)
            Storage->>Postgres: UPDATE processing_jobs SET status='completed'
        else Нет задач
            Note right of Storage: нет доступных задач, ждём
        end
    end

    %% UploadOriginal
    Client->>Storage: UploadOriginal(org_slug?, project_slug?, document_id, filename_extension, content)
    Storage->>Storage: resolve org/project (metadata или умолчания knowledge/documents)
    Storage->>Storage: validate document_id, filename_extension, content
    Storage->>MinIO: ensure bucket knowledge-original доступен
    Storage->>Storage: BuildKey(org/<org>/project/<project>/year=YYYY/month=MM/day=DD/<uuid>/<document_id>.<ext>)
    Storage->>MinIO: PutObject(bucket=knowledge-original, key, ContentType=по расширению)
    MinIO-->>Storage: OK
    Storage-->>Client: bucket/key, created_at (+ response metadata: correlation-id, org-slug, project-slug)

    %% UploadMarkdown
    Client->>Storage: UploadMarkdown(org_slug?, project_slug?, document_id, content)
    Storage->>Storage: resolve org/project (metadata или умолчания)
    Storage->>Storage: validate document_id и content
    Storage->>MinIO: ensure bucket knowledge-md доступен
    Storage->>Storage: BuildKey(org/<org>/project/<project>/year=YYYY/month=MM/day=DD/<uuid>/<document_id>.md)
    Storage->>MinIO: PutObject(bucket=knowledge-md, key, ContentType=text/markdown)
    MinIO-->>Storage: OK
    Storage-->>Client: bucket/key, created_at (+ response metadata)

    %% GetObject
    Client->>Storage: GetObject(bucket, object_key)
    Storage->>MinIO: BucketExists(bucket)
    MinIO-->>Storage: exists
    Storage->>MinIO: GetObject(bucket, key)
    MinIO-->>Storage: bytes
    Storage-->>Client: bucket/key, content, updated_at (+ response metadata)

    %% GenerateUploadURL
    Client->>Storage: GenerateUploadURL(document_id, file_name, content_length, expires_in_seconds?, correlation_id?)
    Storage->>Storage: validate document_id, file_name, content_length > 0
    Storage->>Storage: extension ∈ allowedExtensions (по config, дефолт {doc, docx, pdf}); expires_in_seconds > 0 (или defaultUploadTTL)
    Storage->>MinIO: ensure uploadBucket и markdownBucket доступны
    Storage->>Storage: BuildKey(org=knowledge, project=documents, document_id, ext) с uuid сегментом
    Storage->>Storage: markdown_object_key = key с .md расширением
    Storage->>MinIO: PresignHeader(PUT, bucket=uploadBucket, key, expires, x-amz-meta-correlation-id)
    MinIO-->>Storage: upload_url
    Storage-->>Client: upload_url, bucket, object_key, markdown_bucket, markdown_object_key, expires_in_seconds (+ response metadata: correlation-id, org-slug, project-slug)
```
