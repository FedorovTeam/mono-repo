# Последовательность CreateDocument

```mermaid
sequenceDiagram
    participant C as Клиент
    participant G as gRPC сервер DocumentService
    participant UC as UseCase DocumentService
    participant S as PostgreSQL (documents + processing_jobs)
    participant ST as StorageService (gRPC)
    participant TR as TransformerService (gRPC)
    participant GP as GithubPublisherService (gRPC)
    participant UR as UploadReconciler (cron)
    participant SW as StageWorker (store/convert/publish)
    participant F as JobFollower (cron)
    participant PR as ProcessingReconciler (cron)

    C->>G: CreateDocument(id?, fileName, owner?, repo?, branch?, baseBranch?, path?, upload_bucket/object_key, markdown_bucket/object_key, create_pr?)
    G->>UC: Валидация UUID, обязательных bucket/object_key, owner/repo/branch/path + подстановка defaults
    UC->>S: INSERT document(status=Uploaded, workflow_id=id, original/markdown bucket+object_key, create_pr...), version=1, status_log
    UC-->>C: Document (с заполненными ключами)

    Note over UR,S: UploadReconciler опрашивает Uploaded-документы старше min_uploaded_age и с заполненными original_bucket/object_key
    UR->>G: ConfirmUpload(document_id, bucket, object_key)
    G->>UC: Проверка UUID, обязательных bucket/object_key и соответствия сохранённым значениям
    UC->>ST: ValidateObject/GetObject для проверки доступности исходника
    alt Успешно
        UC->>S: INSERT job store_original(pending, payload=bucket/object_key), event=scheduled; UPDATE document.active_job_id
    else Ошибка постановки задачи
        UC->>S: UPDATE document status=Failed(reason)
    end

    Note over SW,S: StageWorker(store_original) берёт pending store_original и вызывает Activities.MarkStoredOriginal
    SW-->>S: UPDATE document Uploaded->StoredOriginal (idempotent), job_event completed

    Note over F,S: JobFollower читает completed/failed после postpone
    alt store_original completed
        F->>S: INSERT job convert_to_markdown(pending, payload=original_bucket/object_key, markdown_bucket/object_key, correlation_id)
    else convert_to_markdown completed
        F->>S: INSERT job publish_to_github(pending, payload=markdown_bucket/object_key, repo/branch/base_branch/path, create_pr, commit_message/pr_title/pr_body, author_*, correlation_id)
    else любой failed
        F->>S: UPDATE document status=Failed(reason из payload.last_error)
        F->>S: UPDATE processing_jobs status=failed {handled}
    end

    Note over SW,TR: StageWorker(convert_to_markdown) читает документ, резолвит bucket/object_key, добавляет x-correlation-id и вызывает ConvertDocumentToMarkdown(WaitForResult=false)
    alt Статус уже Converted/Published
        SW-->>S: MarkConverted(idempotent), job_event completed
    else gRPC ResourceExhausted
        SW-->>S: UPDATE processing_jobs status=pending, attempts++, payload.last_error, next_run=now()+retry_delay, event postponed
    else status=pending/processing/waiting_docling
        SW-->>S: UPDATE processing_jobs status=pending, payload{queue_id,status,markdown_object_key}, next_run=now()+retry_delay, event postponed
    else Ошибка/пустой markdown_object_key
        SW->>S: UPDATE document status=Failed(reason)
        SW-->>S: UPDATE processing_jobs status=failed, payload.last_error, next_run=now()+retry_delay, event failed
    else status=done и markdown_object_key не пустой
        SW->>S: Activities.MarkConverted -> UPDATE document status=ConvertedToMarkdown(+queue_id, version++)
        SW-->>S: UPDATE processing_jobs status=completed, event completed
    end

    Note over SW,GP: StageWorker(publish_to_github) требует commit_message и author_name/author_email; отправляет PublishMarkdown с x-correlation-id и idempotency_key(если есть markdown_object_key)
    GP-->>SW: status + commit_sha/pull_request*/retry_after/idempotency_hit
    alt success
        SW->>S: UPDATE document status=PublishedToGitHub (+commit_sha)
        SW-->>S: UPDATE processing_jobs status=completed, job_event payload{commit_sha,pull_request*,retry_after?,idempotency_hit?}
    else temporary/permanent failure
        SW->>S: UPDATE document status=Failed(reason от error_code/message)
        SW-->>S: UPDATE processing_jobs status=failed, payload.last_error (+retry_after?), next_run=retry_after or retry_delay, event failed
    end

    Note over PR,S: ProcessingReconciler возвращает зависшие processing-задачи в pending после stage_interval+grace c reason
```
