# Sequence diagram

```mermaid
sequenceDiagram
    participant Client as Клиент / Gateway
    participant Storage as StorageService (gRPC + worker)
    participant Postgres as PostgreSQL
    participant MinIO

    Note over Storage,MinIO: Загрузка config.yaml; uploadBucket = events.upload_bucket или minio.buckets.knowledge_docx (напр. knowledge-original); buckets = {minio.buckets.knowledge_docx, minio.buckets.knowledge_md, uploadBucket (если отличается)}
    Storage->>MinIO: NewMinioClient + initializeBuckets(buckets)
    MinIO-->>Storage: OK
    Note over Storage,MinIO: ConfigureUploadNotification(uploadBucket, events.webhook_arn) (ошибка, если arn не задан)
    Storage->>MinIO: SetBucketNotification(ObjectCreatedAll → targetArn)
    MinIO-->>Storage: OK
    Note over Storage,Postgres: Запуск воркера: подключение processing.dsn, stage=processing.stage (default store_original)
    Storage->>Postgres: pgxpool.New(processing.dsn)
    loop немедленно, затем каждые PollInterval
        Storage->>Postgres: SELECT task pending processing_jobs WHERE stage=processing.stage AND next_run_at<=now() FOR UPDATE SKIP LOCKED
        alt Задача найдена
            Storage->>Postgres: UPDATE processing_jobs SET status='in_progress' + job_events(event_type=processing)
            alt Обновление документа прошло успешно
                Storage->>Postgres: UPDATE documents SET status='StoredOriginal', error_reason=''
                Storage->>Postgres: INSERT document_status_logs(workflow_id из payload.correlation_id, reason='processed by storage worker')
                Storage->>Postgres: UPDATE processing_jobs SET status='completed' + job_events(event_type=completed)
            else Ошибка обновления документа
                Note right of Storage: attempts++, next_run_at = now() + attempts*backoff
                Storage->>Postgres: UPDATE processing_jobs SET status IN ('pending' или 'failed'), payload.last_error
                Storage->>Postgres: INSERT job_events(event_type=retry, message=ошибка)
            end
        else Нет задач
            Note right of Storage: нет доступных задач, ждём
        end
    end

    %% UploadOriginal
    Client->>Storage: UploadOriginal(org_slug?, project_slug?, document_id, filename_extension, content)
    Storage->>Storage: resolve org/project (metadata или умолчания knowledge/documents)
    Storage->>Storage: validate document_id, filename_extension, content
    Storage->>MinIO: ensure bucket knowledge-original доступен (только проверка существования)
    Storage->>Storage: BuildKey(org/<org>/project/<project>/year=YYYY/month=MM/day=DD/<uuid>/<document_id>.<ext>)
    Storage->>MinIO: PutObject(bucket=knowledge-original, key, ContentType=по расширению)
    MinIO-->>Storage: OK
    Storage-->>Client: bucket/key, created_at (+ response metadata: x-correlation-id, org-slug, project-slug)

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
    Storage->>Storage: extension ∈ allowedExtensions (по config или дефолт {doc, docx, pdf})
    Storage->>Storage: expires_in_seconds > 0 (или uploads.signed_url_ttl_seconds, иначе invalid)
    Storage->>Storage: content_length <= uploads.max_size_bytes если задано
    Storage->>MinIO: ensure uploadBucket и markdownBucket доступны
    Storage->>Storage: BuildKey(org=knowledge, project=documents, document_id, ext) с uuid сегментом
    Storage->>Storage: markdown_object_key = key с .md расширением
    Storage->>MinIO: PresignHeader(PUT, bucket=uploadBucket, key, expires, x-amz-meta-correlation-id?)
    MinIO-->>Storage: upload_url
    Storage-->>Client: upload_url, bucket, object_key, markdown_bucket, markdown_object_key, expires_in_seconds (+ response metadata: x-correlation-id, org-slug, project-slug)
```
