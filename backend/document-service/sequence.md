# Последовательность CreateDocument

```mermaid
sequenceDiagram
    participant C as Клиент
    participant G as gRPC сервер DocumentService
    participant S as Бизнес-логика DocumentService
    participant ST as StorageService (gRPC)
    participant TR as TransformerService (gRPC)
    participant GP as GithubPublisherService (gRPC)
    participant R as PostgreSQL (documents + processing_jobs)
    participant UR as UploadReconciler (cron)
    participant PR as ProcessingReconciler (cron)
    participant F as JobFollower (cron)
    participant SW as StageWorker (store/convert/publish)

    C->>G: CreateDocument(id?, fileName, repo?, path?, upload_object_key, markdown_object_key)
    G->>S: Валидация + defaults (owner/repo/branch/base_branch/path, buckets)
    S->>R: INSERT document{status=Uploaded, workflow_id=id, bucket/object_key из запроса}, versions, status_logs
    S-->>C: Document с сохранёнными bucket/object_key

    Note over UR,R: UploadReconciler опрашивает documents со статусом Uploaded, возрастом > minAge и непустыми bucket/object_key
    UR->>G: ConfirmUpload(document_id, bucket, object_key)
    G->>S: Валидация id/bucket/object_key, совпадения с документом
    S->>ST: ValidateObject (GetObject) для проверки доступности объекта
    alt Очередь создана
        S->>R: INSERT job store_original (pending, payload=bucket/object_key) + job_event; document.active_job_id
    else Ошибка постановки задачи
        S->>R: UPDATE document status=Failed(reason)
    end

    Note over SW,R: StageWorker(store_original) берёт pending-задачи store_original
    SW->>S: Activities.MarkStoredOriginal(document_id) -> переход Uploaded->StoredOriginal (idempotent)
    alt Успешно
        SW-->>R: UPDATE processing_jobs status=completed + job_event
    else Ошибка (нет ключа/документ не найден и т.п.)
        SW-->>R: UPDATE processing_jobs status=failed, next_run_at=now()+retry_delay, payload.last_error
    end

    Note over F,R: JobFollower опрашивает completed/failed с паузой postpone
    alt store_original completed
        F->>R: INSERT job convert_to_markdown (pending, payload=orig/markdown buckets+keys, correlation_id)
    else convert_to_markdown completed
        F->>R: INSERT job publish_to_github (pending, payload=markdown_bucket/object_key, repo/branch/base_branch/path, create_pr, commit_message/pr_title/pr_body/author_*, correlation_id)
    else любой failed
        F->>R: UPDATE document status=Failed(reason из payload.last_error)
        F->>R: UPDATE processing_jobs status=failed {handled}
    end

    Note over SW,TR: StageWorker(convert_to_markdown) вызывает transformer-service c WaitForResult=false
    SW->>TR: ConvertDocumentToMarkdown(bucket/object_key + markdown_bucket, markdown_object_key, correlation-id?)
    TR-->>SW: markdown_object_key?, queue_id, status(pending/processing/waiting_docling/done)
    alt status=done и markdown_object_key не пустой
        SW->>S: Activities.MarkConverted -> UPDATE document status=ConvertedToMarkdown (+markdown_queue_id/object_key, version++)
        SW-->>R: UPDATE processing_jobs status=completed + job_event{queue_id,status}
    else status=pending/processing/waiting_docling
        SW-->>R: UPDATE processing_jobs status=pending, next_run_at=now()+retry_delay, payload{queue_id,status,markdown_object_key}
    else gRPC ResourceExhausted (переполнение очереди)
        SW-->>R: UPDATE processing_jobs status=pending, next_run_at=now()+retry_delay, attempts++, payload.last_error=reason + job_event{postponed}
    else Ошибка/пустой markdown_object_key
        S->>R: UPDATE document status=Failed(reason)
        SW-->>R: UPDATE processing_jobs status=failed, next_run_at=now()+retry_delay, payload.last_error=reason
    end

    Note over SW,GP: StageWorker(publish_to_github) требует commit_message и author_name/author_email
    SW->>GP: PublishMarkdown(markdown_bucket/object_key, repo, branch/base_branch, path, create_pr, commit_message, author_*, pr_*, idempotency_key, correlation-id)
    GP-->>SW: status + commit_sha/pr/retry_after/idempotency_hit + error_code?
    alt success
        S->>R: UPDATE document status=PublishedToGitHub (+commit_sha, version?)
        SW-->>R: UPDATE processing_jobs status=completed + job_event{commit_sha,pull_request?,retry_after?,idempotency_hit?}
    else temporary/permanent failure
        S->>R: UPDATE document status=Failed(reason от error_code/message)
        SW-->>R: UPDATE processing_jobs status=failed, next_run_at=retry_after или retry_delay, payload.last_error=reason
    end

    Note over PR,R: ProcessingReconciler возвращает зависшие processing-задачи в pending после stage_interval+grace
```
