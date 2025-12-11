# Последовательность CreateDocument

```mermaid
sequenceDiagram
    participant C as Клиент
    participant G as gRPC сервер DocumentService
    participant S as Бизнес-логика DocumentService
    participant K as KeyManager (MinIO)
    participant R as Репозиторий (PostgreSQL)
    participant UR as Cron Upload Reconciler
    participant MC as Cron Markdown Converter
    participant GPJ as Cron Github Publisher
    participant ST as storage-service
    participant TR as docling-transformer-service
    participant GP as github-publisher-service

    C->>G: CreateDocument(id?, fileName, owner/repository?, branch?, baseBranch?, path?)
    G->>S: Провалидировать вход и defaults
    S->>K: GenerateKeys(documentId, fileName, path, publishPath)
    K-->>S: originalKey + markdownKey
    S->>R: Транзакция: вставка document со статусом Uploaded,
        версия v1 (originalKey/markdownKey), запись в document_status_logs
    S-->>C: Document{status=Uploaded, buckets/object keys}

    Note over UR,R: Cron UploadReconciler ищет Uploaded
    UR-->>ST: StatObject(uploadBucket, uploadObjectKey)
    UR->>G: ConfirmUpload(document_id, bucket, object_key)
    G->>S: Проверка bucket/object_key, UpdateStatus StoredOriginal (+version)
    S->>R: Обновить document_versions, document_status_logs

    Note over MC,R: Cron MarkdownConverter выбирает StoredOriginal
    MC->>ST: GetObject(originalBucket, originalObjectKey)
    MC->>TR: ConvertDocumentToMarkdown
    TR-->>MC: markdown_bucket/object_key
    MC->>R: UpdateStatus ConvertedToMarkdown (version+1, markdown_object_key)

    Note over GPJ,R: Cron GithubPublisher выбирает ConvertedToMarkdown
    GPJ->>ST: Проверка наличия markdown
    GPJ->>GP: PublishMarkdown(owner, repository, branch/baseBranch, path)
    GP-->>GPJ: CommitSha/PR info
    GPJ->>R: UpdateStatus PublishedToGitHub (version+1) либо Failed

    opt Любая ошибка cron-шага
        S/Jobs->>R: UpdateStatus Failed(reason, error_reason)
    end
```
