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
    S-->>S: Проверка bucket/object_key и обновление статуса StoredOriginal
    S->>T: Запуск DocumentProcessingWorkflow(taskQueue, input{bucket'ы, object key, ext, publishPath, version, owner/repo/branch})
    T->>G: Activity PublishMarkdown(commit_message из имени документа, correlation_id=workflow_id, create_pr, content или bucket/object_key)
    T-->>S: Workflow запущен, сохранение workflow_id
```
