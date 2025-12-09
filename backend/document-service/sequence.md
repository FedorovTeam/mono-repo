# Последовательность CreateDocument

```mermaid
sequenceDiagram
    participant C as Клиент
    participant G as gRPC сервер DocumentService
    participant S as Бизнес-логика DocumentService
    participant K as KeyManager (MinIO)
    participant R as Репозиторий (PostgreSQL)
    participant T as Temporal Client
    participant ST as storage-service
    participant TR as docling-transformer-service
    participant GP as github-publisher-service

    C->>G: CreateDocument(id?, fileName, owner?, repository?, branch?, baseBranch?, path?,
    uploadBucket?, uploadObjectKey?, markdownBucket?, markdownObjectKey?, createPr?)
    G->>G: Логирование correlation-id
    G->>S: Вызов CreateDocument с извлечённым расширением файла
    S-->>S: Проверка имени и id (UUID), подстановка owner/repository/branch/baseBranch из config
    S-->>S: Определение bucket'ов (переданные или значения по умолчанию)
    S->>K: GenerateKeys(documentId, publishPath, entitySpecificPath)
    K-->>S: originalKey, markdownKey (<prefix>/year=YYYY/month=MM/day=DD/<document_id>/<uuid>[.<ext>]/...)
    S-->>S: Выбор итоговых object key (переданные или сгенерированные)
    S->>R: Транзакция: вставка document со статусом Uploaded,
    версия v1, запись в document_status_logs, workflow_id = documentId
    R-->>S: Сохранённый документ
    S-->>C: Document{status=Uploaded, bucket'ы и object key}

    Note over C,G: После загрузки файла внешний триггер (например,
    событие ObjectCreated в MinIO или вызов storage-service)
    дергает ConfirmUpload
    C-->>G: ConfirmUpload(document_id, bucket, object_key)
    G->>G: Логирование correlation-id
    G->>S: ConfirmUpload(document_id, bucket, object_key)
    S-->>S: Проверка bucket/object_key, возврат документа
    S->>T: Start DocumentProcessingWorkflow(ID=document_id, taskQueue из config)
    alt Ошибка запуска
        S->>R: UpdateStatus Failed (reason=start workflow)
        S-->>C: Ошибка
    else Успех запуска
        T-->>S: RunID сохранён в workflow_id
    end

    Note over T,R: Перед каждой активностью сверяется текущее состояние
    документа в PostgreSQL — уже выполненные шаги пропускаются.
    T->>S: Activity AwaitOriginal → storage-service.GetObject(bucket, object_key)
    S->>R: UpdateStatus StoredOriginal, версия сохранена
    T->>TR: Activity ConvertToMarkdown(originalBucket/objectKey → markdownBucket)
    TR-->>T: MarkdownContent? + markdown_object_key
    S->>R: UpdateStatus ConvertedToMarkdown c новым markdown_object_key
    T->>GP: Activity PublishMarkdown(bucket/object_key, create_pr, commit_message, correlation_id=workflowID)
    GP-->>T: Publish result (CommitSha)
    S->>R: UpdateStatus PublishedToGitHub
    opt Любая ошибка активити
        S->>R: UpdateStatus Failed(reason, error_reason)
    end
```
