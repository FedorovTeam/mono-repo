# Последовательность CreateDocument

```mermaid
sequenceDiagram
    participant C as Клиент
    participant G as gRPC сервер DocumentService
    participant S as Бизнес-логика DocumentService
    participant K as KeyManager (MinIO)
    participant R as Репозиторий (PostgreSQL)
    participant T as Temporal Client
    participant UR as Upload Reconciler
    participant WR as Workflow Retrier
    participant ST as storage-service
    participant TR as docling-transformer-service
    participant GP as github-publisher-service

    C->>G: CreateDocument(id?, fileName, owner?, repository?, branch?, baseBranch?, path?,
    uploadBucket?, uploadObjectKey?, markdownBucket?, markdownObjectKey?, createPr?, entitySpecificPath?)
    G->>G: Логирование correlation-id
    G->>S: Вызов CreateDocument с извлечённым расширением файла
    S-->>S: Проверка имени и id (UUID)
    S-->>S: Owner/repository могут быть заданы как owner/repo; иначе подставляются значения из конфигурации
    S-->>S: branch и baseBranch подставляются из config (если baseBranch не задан, берётся branch)
    S-->>S: Path по умолчанию = fileName, publishPath = path
    S-->>S: Определение bucket'ов (переданные или значения по умолчанию; отсутствие — ошибка)
    S->>K: GenerateKeys(documentId, fileName, path, publishPath, entitySpecificPath)
    K-->>S: originalKey, markdownKey (<prefix>/year=YYYY/month=MM/day=DD/<document_id>/<uuid>[.<ext>]/...)
    S-->>S: Выбор итоговых object key (переданные или сгенерированные)
    S->>R: Транзакция: вставка document со статусом Uploaded,
    версия v1 (originalKey/markdownKey), запись в document_status_logs, workflow_id = documentId
    R-->>S: Сохранённый документ
    S-->>C: Document{status=Uploaded, buckets/object keys, defaults подставлены}

    Note over C,G: После загрузки файла внешний триггер (например,
    событие ObjectCreated в MinIO или вызов storage-service)
    дергает ConfirmUpload
    C-->>G: ConfirmUpload(document_id, bucket, object_key)
    G->>G: Логирование correlation-id
    G->>S: ConfirmUpload(document_id, bucket, object_key)
    S-->>S: Проверка bucket/object_key (должны совпасть с сохранёнными)
    S->>T: Start DocumentProcessingWorkflow(ID=document_id, taskQueue из config)
    alt Ошибка запуска
        S->>R: UpdateStatus Failed (reason=start workflow)
        S-->>C: Ошибка
        WR-->>R: Периодический поиск Failed (start workflow)
        WR->>T: Start DocumentProcessingWorkflow(ID=document_id, correlation_id=workflow_id)
        T-->>WR: RunID сохранён в workflow_id
    else Успех запуска
        T-->>S: RunID сохранён в workflow_id
    end

    Note over T,R: Перед каждой активностью сверяется текущее состояние
    документа в PostgreSQL — уже выполненные шаги пропускаются.

    T->>S: Activity AwaitOriginal → storage-service.GetObject(bucket, object_key)
    S->>R: UpdateStatus StoredOriginal (версия >=1)

    T->>TR: Activity ConvertToMarkdown(originalBucket/objectKey → markdownBucket, markdownKey)
    TR-->>T: status (done/queued/..), markdown_object_key?, queue_id
    S->>R: UpdateStatus ConvertedToMarkdown (version+1, markdown_object_key/queue_id)

    opt Transformer вернул status=done
        Note over T: AwaitMarkdown пропускается
    else status != done
        T->>S: Activity AwaitMarkdown(markdown_bucket, markdown_object_key, queue_id, correlation_id)
        S-->>ST: storage-service.GetObject с экспоненциальным бэкоффом до 15 минут
        alt Markdown не появился
            S->>R: UpdateStatus Failed (conversion notification timeout)
        end
    end

    T->>S: Activity PublishMarkdown(correlation_id=workflowID, create_pr)
    S-->>ST: Проверка наличия markdown объекта (до 3 попыток с бэкоффом)
    S-->>GP: PublishMarkdown(owner, repository, branch/baseBranch, path, commitMessage)
    GP-->>S: Publish result (CommitSha)
    S->>R: UpdateStatus PublishedToGitHub (version+1)

    par Фоновые задачи
        UR-->>R: Поиск Uploaded старше minAge, stat в MinIO
        UR->>S: ConfirmUpload(document_id, bucket, object_key)

        WR-->>R: Периодический поиск Failed(start workflow)
        WR->>T: Start DocumentProcessingWorkflow(ID=document_id, correlation_id=workflow_id)
    end

    opt Любая ошибка активити
        S->>R: UpdateStatus Failed(reason, error_reason)
    end
```
