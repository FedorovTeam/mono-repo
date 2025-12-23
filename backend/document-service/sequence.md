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

    C->>G: CreateDocument(id?, fileName, owner?, repository, branch?, baseBranch?, path?, upload_bucket/object_key, markdown_bucket/object_key, conversion_strategy?, extract_tables?, create_pr?)
    G->>UC: Валидация UUID/обязательных полей + подстановка defaults (owner/repository/branch/base_branch/path/buckets/strategy/extract_tables)
    UC->>S: INSERT document(status=Uploaded, workflow_id=id, original/markdown bucket+object_key, create_pr/strategy/extract_tables...), version=1, status_log
    UC-->>C: Document (workflow_id=id, с заполненными ключами)

    Note over UR,S: UploadReconciler ищет Uploaded-документы старше min_uploaded_age с original_bucket/object_key
    UR->>G: ConfirmUpload(document_id, bucket, object_key, overrides: conversion_strategy?, extract_tables?)
    G->>UC: Проверка UUID, bucket/object_key и совпадения с документом
    UC->>ST: ValidateObject(bucket, object_key) -> etag
    alt Постановка store_original удалась
        UC->>S: INSERT processing_job(stage=store_original,status=pending,payload=bucket/object_key/etag), job_event scheduled
    else Ошибка добавления задачи
        UC->>S: UPDATE document status=Failed(reason)
    end

    Note over SW,S: StageWorker(store_original) -> Activities.MarkStoredOriginal -> Transition Uploaded->StoredOriginal (idempotent), status_log, job_event completed

    Note over F,S: JobFollower читает completed/failed после postpone окна
    alt stage=store_original completed
        Note right of F: StageRouter.NextStageAfterStore<br/>- strategy=docling_with_tables -> convert_to_markdown<br/>- extract_tables=true или strategy=tables_only при включённом table_extractor и подходящем расширении -> extract_tables<br/>- иначе если preprocess.enabled и расширение в списке -> preprocess_to_docx<br/>- иначе convert_to_markdown
        alt next=extract_tables
            F->>S: INSERT job extract_tables(pending, payload=original_bucket/object_key, artifacts_bucket/prefix, markdown_bucket/object_key, correlation_id=workflow_id)
        else next=preprocess_to_docx
            F->>S: INSERT job preprocess_to_docx(pending, payload=original_bucket/object_key, original_etag, correlation_id=workflow_id)
        else next=convert_to_markdown
            F->>S: INSERT job convert_to_markdown(pending, payload=original_bucket/object_key, markdown_bucket/object_key, correlation_id=workflow_id)
        end
    else stage=preprocess_to_docx completed
        F->>S: INSERT job convert_to_markdown(pending, payload=preprocessed_bucket/object_key (+preprocess_job_id/target_key), markdown_bucket/object_key, correlation_id=workflow_id)
    else stage=convert_to_markdown completed
        alt router.ShouldExtractTablesAfterConvert(extract_tables flag/strategy tables_only/docling_with_tables или table_count/pages >= TableDetectionMinCount с разрешённым расширением)
            F->>S: INSERT job extract_tables(pending, payload=original_bucket/object_key, artifacts_bucket/prefix, markdown_bucket/object_key, docling_markdown_* из convert, correlation_id=workflow_id)
        else
            F->>S: INSERT job publish_to_github(pending, payload=markdown_bucket/object_key, repo/branch/base_branch/path, create_pr, commit_message/pr_title/pr_body, author_*, correlation_id=workflow_id)
        end
    else stage=extract_tables completed
        F->>S: INSERT job publish_to_github(pending, payload=markdown_bucket/object_key, repo/branch/base_branch/path, create_pr, commit_message/pr_title/pr_body, author_*, correlation_id=workflow_id)
    else любой failed
        F->>S: UPDATE document status=Failed("stage " + job.stage + " failed: " + payload.last_error)
        F->>S: UPDATE processing_jobs status=failed {handled}
    end

    Note over SW,ST: StageWorker(preprocess_to_docx) отправляет SubmitPreprocess/ polling GetPreprocessStatus; pending/processing -> pending с next_run=now()+preprocess_retry_delay; retryable gRPC ошибки -> pending с экспоненциальным backoff; FAILED -> job_failed (document упадёт через JobFollower); COMPLETED -> job_event completed
    Note over SW,TE: StageWorker(extract_tables) вызывает ExtractTablesFromPdf при отсутствии queue_id, иначе GetTableExtractionJob; pending/processing -> pending с backoff; FAILED -> job_failed; DONE -> Activities.MergeTables (если есть базовый markdown в storage) и Activities.MarkConverted(status=ConvertedToMarkdown, version++), job_event completed
    Note over SW,TR: StageWorker(convert_to_markdown) собирает источники (preprocessed или original), вызывает ConvertDocumentToMarkdown(WaitForResult=false)<br/>- not ready -> pending с retry_delay<br/>- временная gRPC ошибка -> pending с backoff и attempts++ (max 5 => statuses.Fail для документа)<br/>- ready + требуется цепочка таблиц (router.ShouldExtractTablesAfterConvert) -> job_event completed без смены статуса<br/>- ready без таблиц -> Activities.MarkConverted -> status=ConvertedToMarkdown (+queue_id/table metadata/version), job_event completed; ошибки/пустой markdown_key -> job_failed
    Note over SW,GP: StageWorker(publish_to_github) требует commit_message и author_*; вызывает PublishMarkdown; success -> status=PublishedToGitHub (+commit_sha, version опционально), job_event completed; временные gRPC ошибки -> pending с backoff/attempts++; иные ошибки/валидация -> job_failed (JobFollower пометит документ Failed)
    Note over PR,S: ProcessingReconciler возвращает зависшие in_progress задачи в pending после stage_interval+grace с reason и увеличением attempts
    Note over G,S: UpdateDocumentStatus(gRPC) при переходах StoredOriginal/ConvertedToMarkdown планирует следующую задачу через StageRouter (convert/extract/publish)
```
