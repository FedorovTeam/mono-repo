# Последовательность CreateDocument

```mermaid
sequenceDiagram
    participant C as Клиент
    participant G as gRPC сервер DocumentService
    participant S as Бизнес-логика DocumentService
    participant K as KeyManager (MinIO)
    participant R as Репозиторий (PostgreSQL)
    participant T as Temporal Client
    participant M as MinIO
    participant ST as storage-service
    participant TR as docling-transformer-service
    participant GP as github-publisher-service

    C->>G: CreateDocument(id?, fileName, owner?, repository, branch, baseBranch?, path,
    uploadBucket?, uploadObjectKey?, markdownBucket?, markdownObjectKey?, contentLength, createPr?)
    G->>G: Логирование correlation-id
    G->>S: Вызов CreateDocument с извлечённым расширением файла
    S-->>S: Проверка имени и id (UUID)
    S-->>S: Определение bucket'ов (заданые или значения по умолчанию)
    S->>K: GenerateKeys(documentId, publishPath)
    K-->>S: originalKey, markdownKey (<prefix>/year=YYYY/month=MM/day=DD/<document_id>/<entity_path>/...)
    S-->>S: Выбор итоговых object key (переданные или сгенерированные)
    S->>R: Транзакция: вставка document со статусом Uploaded,
    версия v1, запись в document_status_logs
    R-->>S: Сохранённый документ
    S-->>C: Document{status=Uploaded, object key, bucket'ы}

    M-->>G: Event ObjectCreated(bucket=knowledge-docx, object_key)
    G->>S: ConfirmUpload(document_id, bucket, object_key)
    S-->>S: Проверка bucket/object_key, возврат документа и запуск Workflow (ID=document_id, taskQueue из config)
    S->>T: Start DocumentProcessingWorkflow(document_id)
    T->>S: Activity AwaitOriginal → GetObject в storage-service, статус StoredOriginal в БД
    T->>TR: Activity ConvertToMarkdown(bucket/object_key) с correlation-id
    TR-->>T: MarkdownContent + markdown_object_key
    T->>S: Update статус ConvertedToMarkdown, сохранение версии
    T->>GP: Activity PublishMarkdown(bucket/object_key или content, create_pr, commit_message)
    GP-->>T: Publish result
    T->>S: Update статус PublishedToGitHub, reason при ошибке = Failed
    T-->>S: Workflow завершён, RunID сохранён
```
