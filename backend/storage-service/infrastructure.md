# Инфраструктура

```mermaid
sequenceDiagram
    participant Client as gRPC клиент
    participant Storage as storage-service
    participant MinIO as MinIO
    participant Webhook as Webhook/SQS -> gateway

    rect rgb(245,245,245)
        note over Storage: Старт
        Storage->>MinIO: Инициализировать бакеты<br/>knowledge-original, knowledge-md,<br/>events.upload_bucket (если отличается)
        Storage->>MinIO: Настроить уведомления ObjectCreated<br/>для events.upload_bucket в events.webhook_arn
    end

    Client->>Storage: GenerateUploadURL(document_id,<br/>file_name, content_length, correlation_id)
    Storage->>MinIO: Presign PUT для events.upload_bucket<br/>c заголовком x-amz-meta-correlation-id
    MinIO-->>Storage: upload_url + object_key
    Storage-->>Client: upload_url, bucket, object_key<br/>+ метаданные x-correlation-id/org-slug/project-slug

    Client->>MinIO: PUT объект по upload_url
    MinIO-->>Webhook: s3:ObjectCreated:* событие
    Webhook-->>Gateway: уведомление о новом объекте
```

- Все бакеты (`knowledge-original`, `knowledge-md` и кастомный `events.upload_bucket`) создаются при старте, если отсутствуют, и остаются приватными.
- Если `events.upload_bucket` не задан, для подписанных ссылок используется `minio.buckets.knowledge_docx`; при другом значении он добавляется к списку инициализируемых бакетов.
- Ключи объектов формируются по шаблону `org/<org_slug>/project/<project_slug>/year=YYYY/month=MM/day=DD/<uuid>/<document_id>.<ext>` с обязательным промежуточным UUID.
- `GenerateUploadURL` подписывает PUT-запросы MinIO с заголовком `x-amz-meta-correlation-id`, чтобы сохранить трассировку загрузки.
- Уведомления MinIO о загрузках (`s3:ObjectCreated:*`) перенаправляются в gateway через ARN `events.webhook_arn`.
