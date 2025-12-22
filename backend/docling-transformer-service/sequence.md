# Sequence diagram

Диаграммы ниже отражают однопроходную конвертацию без вспомогательных фаз и ручного мерджа таблиц.

```mermaid
sequenceDiagram
    participant GW as Gateway/cron job
    participant TR as Transformer gRPC
    participant PG as PostgreSQL Queue
    participant WK as Worker/Maintenance
    participant MN as MinIO
    participant DL as Docling HTTP

    GW->>TR: ConvertDocumentToMarkdown(document_id, original_bucket, original_object_key,<br/>source_bucket/object_key | source_url,<br/>markdown_bucket?, markdown_object_key?, filename_extension?, correlation_id)
    TR-->>TR: correlation_id из метаданных gRPC или сгенерированный
    TR->>TR: валидация document_id/original_* <br/>source_bucket и source_object_key должны быть вместе <br/>source_url взаимоисключается с bucket/object key
    TR->>TR: markdown_bucket по умолчанию из конфигурации (ошибка, если пустой)
    TR->>TR: NormalizeExtension(filename_extension | original_object_key) + ResolveContentTypeByExtension
    TR->>TR: markdown_object_key по умолчанию через BuildMarkdownKey(original_object_key)
    TR->>TR: проверка лимита очереди (CountByStatus)
    TR->>PG: GetLatestByDocument(document_id)
    alt найдена неfailed задача
        TR-->>GW: reuse queue_id/status + ResolveOutput(markdown_bucket/object_key) с учётом сохранённых значений
    else новая задача
        TR->>PG: Enqueue(status=pending, markdown_bucket/object_key,<br/>source_bucket/object_key или source_url, correlation_id)
        TR-->>GW: queue_id, status=pending, markdown_bucket/object_key, correlation_id
    end
    opt wait_for_result=true
        loop WaitForResult (backoff из status_backoff + task.next_poll_at)
            TR->>PG: GetByID(queue_id)
            alt status in [done, failed]
                TR-->>GW: финальный status + ResolveOutput(markdown_bucket/object_key)
            else
                TR-->>TR: ожидание до next_poll_at или backoff
            end
        end
        TR-->>GW: DeadlineExceeded при исчерпании попыток
    end

    loop workerLoop (MaxWorkers)
        WK->>PG: AcquireBatch(BatchSize) // waiting_docling с наступившим next_poll_at → первыми при наличии любых waiting_docling pending блокируются
        alt задача с docling_task_id/job_id
            WK->>DL: GET /v1/status/poll/{task_or_job_id} (X-Correlation-Id)
            WK->>PG: UpdateDoclingInfo(docling_task_id/job_id, docling_poll_count++)
            alt status/result готов
                WK->>MN: PUT markdown_object_key в markdown_bucket с media_type
                WK->>PG: MarkDone(status=done, finished_at)
            else статус pending/временная ошибка
                alt превышен лимит рестартов
                    WK->>PG: MarkFailed(status=failed, error=docling restarts exceeded)
                else превышен лимит поллинга (до/после запроса) или повтор ошибок
                    WK->>PG: RequeueDoclingWaiting(status=pending, retry_count++, reset docling ids/poll_count,<br/>next_poll_at=requeue_delay, clear docling ids)
                else
                    WK->>PG: MarkDoclingWaiting(status=waiting_docling, next_poll_at=exp.backoff)
                end
            end
        else новая конвертация
            WK-->>WK: подставить markdown_bucket/object_key из конфигурации при необходимости
            WK-->>WK: определить источник (source_async|download) по docling.mode и наличию source_url
            alt submit_lock занят
                WK->>PG: ReschedulePending(status=pending, next_poll_at=submit_lock_delay)
            else
                alt useSourceAsync (mode=source_async, source_url пуст,<br/>использует source_bucket/object_key либо original_bucket/object_key)
                    WK-->>DL: POST /v1/convert/source/async (JSON: source_bucket|original_bucket,<br/>target markdown_bucket/object_key, to_formats/from_formats)
                else синхронная загрузка
                    alt источник URL
                        WK->>DL: POST /v1/convert/file/async (multipart, скачанный контент, X-Docling-Source-Filename)
                    else объект в MinIO (source_bucket/object_key или original_bucket/object_key)
                        WK->>MN: GET исходный объект
                        WK->>DL: POST /v1/convert/file/async (multipart контент)
                    end
                end
                alt docling вернул временную ошибку после всех ретраев submit
                    WK->>PG: MarkDoclingWaiting(status=waiting_docling, docling_task_id/job_id пустые,<br/>docling_poll_count сохраняется, next_poll_at=docling backoff)
                else успешная постановка docling
                    WK->>PG: MarkDoclingWaiting(status=waiting_docling, docling_task_id/job_id,<br/>docling_poll_count=0, next_poll_at=initial backoff)
                end
            end
        end
        alt ошибка обработчика
            WK->>PG: MarkFailed(status=failed, finished_at, error)
        end
    end

    loop maintenanceLoop
        WK->>PG: CountByStatus // метрики очереди
        WK->>PG: ActivateWaiting(now, max_docling_lifetime) // только помечает waiting_docling просроченные как failed
        WK->>PG: RestoreStuckProcessing(processing_timeout, max_retries, max_docling_attempts) // pending или failed при превышении лимитов, сброс docling_* и next_poll_at
        WK->>PG: RetryFailed(olderThan=now-failed_retry_delay, max_failed_retries) // вернуть в pending или оставить failed при превышении лимита
        WK->>PG: CleanupFinished(retention)
    end
```

```mermaid
sequenceDiagram
    participant CL as Client/gRPC caller
    participant TR as Transformer gRPC
    participant SRC as HTTP Source (docx_url)
    participant DL as Docling HTTP

    CL->>TR: ConvertDocxToMarkdown(docx_content|docx_url, filename?, correlation_id)
    alt docx_content и docx_url заданы одновременно или оба пустые
        TR-->>CL: INVALID_ARGUMENT
    else валидный источник
        TR-->>TR: correlation_id || EnsureCorrelationID
        alt указан docx_url
            TR->>SRC: GET docx_url (timeout ~30s)
            TR-->>TR: filename из URL, запасной document.docx
        else бинарное содержимое
            TR-->>TR: filename из запроса или document.docx
        end
        TR->>TR: ResolveContentTypeByExtension(filename)
        TR->>DL: POST /v1/convert/file/async (multipart, X-Correlation-Id, X-Docling-Source-Filename)
        loop pollUntilReady (status_backoff)
            TR->>DL: GET /v1/status/poll/{job_id}
            alt результат готов
                TR->>DL: GET /v1/result/{job_id}
                TR-->>CL: markdown + media_type + charset + correlation_id
            else
                TR-->>TR: ожидание c ростом интервала
            end
        end
    end
    TR-->>CL: DeadlineExceeded если попытки исчерпаны или контекст отменён
```
