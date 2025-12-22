# Последовательность обработки документа

```mermaid
sequenceDiagram
    participant C as Клиент
    participant G as gRPC сервер DocumentService
    participant UC as UseCase DocumentService
    participant S as PostgreSQL (documents + processing_jobs)
    participant ST as StorageService (gRPC)
    participant TR as TransformerService (gRPC)
    participant TE as TableExtractorService (gRPC)
    participant GP as GithubPublisherService (gRPC)
    participant UR as UploadReconciler (cron)
    participant SW as StageWorker (store/preprocess/extract/convert/publish)
    participant F as JobFollower (cron)
    participant PR as ProcessingReconciler (cron)

    C->>G: CreateDocument(id?, fileName, owner?, repository, branch?, baseBranch?, path?, upload_bucket/object_key, markdown_bucket/object_key, create_pr?)
    G->>UC: Валидация UUID, bucket/object_key, owner/repository/branch/path + подстановка defaults
    UC->>S: INSERT document(status=Uploaded, workflow_id=id, original/markdown bucket+object_key, create_pr...), version=1, status_log
    UC-->>C: Document (workflow_id=id, с заполненными ключами)

    Note over UR,S: UploadReconciler выбирает Uploaded-документы старше min_uploaded_age с original_bucket/object_key
    UR->>G: ConfirmUpload(document_id, bucket, object_key)
    G->>UC: Проверка UUID, bucket/object_key и совпадения с документом
    UC->>ST: ValidateObject(bucket, object_key)
    alt Постановка store_original удалась
        UC->>S: INSERT processing_job(stage=store_original,status=pending,payload=bucket/object_key), job_event scheduled UPDATE document.active_job_id
    else Ошибка добавления задачи
        UC->>S: UPDATE document status=Failed(reason)
    end

    Note over SW,S: StageWorker(store_original) берёт pending store_original через WorkerService
    SW->>S: Activities.MarkStoredOriginal -> Transition Uploaded->StoredOriginal (idempotent), status_log job_event completed

    Note over F,S: JobFollower читает completed/failed после postpone окна
    alt store_original completed
        alt router.table_extractor.enabled AND (extract_tables=true OR conversion_strategy=tables_only) AND ext in table_extractor_extensions
            F->>S: INSERT job extract_tables(pending, payload=original_bucket/object_key, artifacts_bucket/prefix, correlation_id=workflow_id)
        else preprocess.enabled AND ext in preprocess_extensions
            F->>S: INSERT job preprocess_to_docx(pending, payload=original_bucket/object_key, correlation_id=workflow_id)
        else docx/other
            F->>S: INSERT job convert_to_markdown(pending, payload=original_bucket/object_key, markdown_bucket/object_key, correlation_id=workflow_id)
        end
    else preprocess_to_docx completed
        F->>S: INSERT job convert_to_markdown(pending, payload=preprocessed_bucket/object_key?, markdown_bucket/object_key, correlation_id=workflow_id)
    else extract_tables completed
        F->>S: INSERT job publish_to_github(pending, payload=markdown_bucket/object_key, repo/branch/base_branch/path, create_pr, commit_message/pr_title/pr_body, author_*, correlation_id=workflow_id)
    else convert_to_markdown completed
        F->>S: INSERT job publish_to_github(pending, payload=markdown_bucket/object_key, repo/branch/base_branch/path, create_pr, commit_message/pr_title/pr_body, author_*, correlation_id=workflow_id)
    else любой failed
        F->>S: UPDATE document status=Failed("stage " + job.stage + " failed: " + payload.last_error)
        F->>S: UPDATE processing_jobs status=failed {handled}
    end

    Note over SW,S: StageWorker(preprocess_to_docx) опрашивает preprocess-service; при статусах pending/processing переносит задачу в pending с next_run=now()+preprocess_retry_delay, временные gRPC ошибки используют backoff от того же интервала
    Note over SW,TE: StageWorker(extract_tables) вызывает ExtractTablesFromPdf(wait=false), сохраняет queue_id/artifacts_prefix, по статусам pending/processing/failed_retryable откладывает задачу с backoff, при status=done помечает документ ConvertedToMarkdown с markdown_object_key=<artifacts_prefix>/tables.md
    Note over SW,TR: StageWorker(convert_to_markdown) заполняет payload из документа, при наличии preprocessed_bucket/object_key использует их как источник, выставляет x-correlation-id и вызывает ConvertDocumentToMarkdown(WaitForResult=false) при success пишет job_event со ссылкой на источник и preprocess_job_id
    alt Документ уже Converted/Published
        SW-->>S: Activities.MarkConverted (no-op), job_event completed
    else Временная gRPC ошибка (Unavailable/ResourceExhausted/...)
        SW-->>S: UPDATE processing_jobs status=pending, attempts++, payload.last_error, next_run=now()+retry_backoff, event postponed max_attempts => statuses.Fail
    else Ответ pending/processing/waiting*
        SW-->>S: UPDATE processing_jobs status=pending, payload{queue_id,status,markdown_object_key}, next_run=now()+retry_delay, event postponed
    else Ошибка/пустой markdown_key
        SW-->>S: UPDATE processing_jobs status=failed, payload.last_error, event failed (document упадёт через JobFollower)
    else status=done и markdown_key получен
        SW->>S: Activities.MarkConverted -> UPDATE document status=ConvertedToMarkdown (+version, queue_id), status_log
        SW-->>S: UPDATE processing_jobs status=completed, job_event completed
    end

    Note over SW,GP: StageWorker(publish_to_github) требует commit_message и author_name/email формирует idempotency_key при наличии markdown_object_key
    SW->>GP: PublishMarkdown(bucket/object_key, owner/repo, branch/base_branch, path, create_pr, commit_message, author_*, pr_title/body, correlation_id, idempotency_key)
    GP-->>SW: status + commit_sha/pull_request*/retry_after/idempotency_hit
    alt success
        SW->>S: Activities.PublishMarkdown -> UPDATE document status=PublishedToGitHub (+commit_sha), status_log
        SW-->>S: UPDATE processing_jobs status=completed, job_event payload{commit_sha,pull_request*,retry_after?,idempotency_hit?}
    else Временная gRPC ошибка
        SW-->>S: UPDATE processing_jobs status=pending, attempts++, payload.last_error, next_run=now()+retry_backoff, event postponed max_attempts => statuses.Fail
    else Ответ с ошибкой/валидация payload
        SW-->>S: UPDATE processing_jobs status=failed, payload.last_error(+retry_after?), event failed (document отмечается Failed через JobFollower)
    end

    Note over PR,S: ProcessingReconciler возвращает зависшие in_progress задачи в pending после stage_interval+grace с reason и увеличением attempts
    Note over G,S: UpdateDocumentStatus(gRPC) при переходах StoredOriginal/ConvertedToMarkdown сам планирует следующую задачу (convert/publish)
```
