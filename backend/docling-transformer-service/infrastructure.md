# Infrastructure view

```mermaid
sequenceDiagram
    participant GW as Gateway / Workflows-клиенты
    participant API as Transformer gRPC API
    participant PG as PostgreSQL<br/>transformations_queue
    participant WRK as QueueWorker + Processor
    participant MN as MinIO buckets
    participant DL as Docling Serve
    participant OBS as OTel Collector (opt.)

    Note over API,OBS: observability.* включает трассировки/метрики/логи<br/>для gRPC и фоновых воркеров

    GW->>API: gRPC ConvertDocumentToMarkdown<br/>:service.grpc_port (9090)
    alt запись уже есть (не failed)
        API->>PG: SELECT latest task by document_id
    else новая постановка
        API->>PG: INSERT pending (queue_limit)
    end
    API-->>GW: queue_id + status + markdown bucket/key<br/>+ correlation_id (+table_metadata если готово)
    opt wait_for_result
        loop status_backoff (docling.*)
            API->>PG: poll status/next_poll_at
            PG-->>API: waiting_docling/done/failed
        end
    end

    Note over WRK,PG: RestoreProcessing on startup;<br/>pending не выдаются, пока есть waiting_docling
    loop scheduler.poll_interval
        WRK->>PG: SELECT waiting_docling ready<br/>FOR UPDATE SKIP LOCKED
        alt нет готовых waiting_docling
            WRK->>PG: SELECT pending ... LIMIT batch_size<br/>(только если waiting_docling нет совсем)
        end
        opt submit to Docling
            WRK->>PG: pg_try_advisory_lock(docling_submit_lock)
            alt lock busy
                WRK->>PG: UPDATE pending next_poll_at<br/>= now()+submit_lock_delay
            else lock ok
                WRK->>MN: download original/source<br/>или HTTP GET source_url
                WRK->>DL: POST /v1/convert/file/async<br/>или /v1/convert/source/async
                WRK->>PG: UPDATE waiting_docling + next_poll_at
            end
        end
        opt poll Docling
            WRK->>DL: GET /v1/status/poll/{task_id}
            alt статус pending/ошибка
                WRK->>PG: UPDATE waiting_docling + next_poll_at
                opt лимит опросов/повторные ошибки
                    WRK->>PG: REQUEUE pending, сброс docling_id
                end
            else результат готов
                WRK->>DL: GET /v1/result/{task_id}
                WRK->>MN: upload markdown<br/>(Docling может писать TargetBucket напрямую)
                WRK->>PG: UPDATE done + table_metadata
            end
        end
    end

    par scheduler.maintenance_interval
        WRK->>PG: CountByStatus (метрики)
        WRK->>PG: expire waiting_docling by max_job_lifetime
        WRK->>PG: RestoreStuckProcessing<br/>(processing_timeout, docling.status_max_attempts)
        WRK->>PG: RetryFailed (delay/max_retries)
        WRK->>PG: CleanupFinished (retention)
    end

    GW->>API: gRPC ConvertDocxToMarkdown (sync)
    API->>DL: submit + poll Docling<br/>без очереди и MinIO
    API-->>GW: markdown + media_type/charset + correlation_id
```

Docling вызывается в одном проходе: после успешного ответа результат сразу сохраняется без дополнительных фаз и ручного мерджа.

- gRPC сервер слушает порт из `service.grpc_port` (в `config/config.yaml` по умолчанию `9090`) и принимает:
  - `ConvertDocumentToMarkdown` — ищет последнюю задачу по `document_id` и, если она не в `failed`, возвращает её со скорректированными `markdown_bucket/object_key` без повторной постановки. Иначе, учитывая `service.queue_limit`, добавляет новую запись в `transformations_queue`. В ответе всегда отдают `queue_id`, `status`, `correlation_id`, расчётный bucket/key и (когда готово) `table_metadata/table_count/table_pages`. При `wait_for_result` API опрашивает статус в PostgreSQL по бэкоффу `docling.*` и возвращает ошибку, если лимит попыток исчерпан.
  - `ConvertDocxToMarkdown` — синхронно проксирует docx-файл (или скачивает `docx_url`) в Docling и возвращает готовый Markdown без постановки в очередь и без MinIO.
- Воркер (QueueWorker/Processor) восстанавливает незавершённые `processing` в `pending` при старте, затем забирает задачи: сначала готовые `waiting_docling`, а пока в очереди есть любые `waiting_docling`, `pending` не выдаются (сериализация Docling вызовов). Перед отправкой берётся advisory-замок `pg_try_advisory_lock`; при занятости задача рескейлится с новым `next_poll_at`. Источник берётся из `source_url` (HTTP), `source_bucket/object_key` или из оригинала; в `source_async` Docling получает ссылки и может писать результат сразу в TargetBucket/ObjectKey. Доклинг-поллер учитывает лимиты опросов/рестартов (`docling.status_max_attempts`, `scheduler.docling_max_restarts`) и при повторяющихся ошибках переводит задачу обратно в `pending`, сбрасывая `docling_task_id`. Готовый Markdown сохраняется в MinIO (если Docling не писал сам), а метаданные таблиц попадают в `table_metadata/table_count/table_pages`.
- Docling вызывается по HTTP `POST /v1/convert/file/async` с формой `files` либо `POST /v1/convert/source/async`, куда пробрасываются `X-Correlation-Id`, исходное имя файла (`X-Docling-Source-Filename`), выбранный эндпоинт (`X-Docling-Endpoint`), `X-Api-Key` (если нужен) и минимальные параметры формата (`to_formats`, `from_formats`). Результат запрашивается через `/v1/status/poll/{task_id}` и `/v1/result/{task_id}`. Для `source_async` Docling использует учётные данные `docling.storage.*` для доступа к MinIO: читает исходный объект и при наличии `TargetBucket/ObjectKey` может писать результат напрямую в указанный бакет.
- Планировщик обслуживания (`MaintenanceRunner`) фиксирует размер очереди, помечает просроченные `waiting_docling` как `failed` (`docling.max_job_lifetime`), восстанавливает или завершает зависшие `processing` (`service.processing_timeout`, `docling.status_max_attempts`), перезапускает `failed` после `scheduler.failed_retry_delay` до достижения `scheduler.max_failed_retries` и чистит устаревшие `done/failed` по `cleanup_retention`.
- Вызовы приходят от cron-воркеров `document-service`, которые по статусам в PostgreSQL решают, какой документ нужно конвертировать; сам сервис не хранит состояния workflow.
- Трассировка, метрики и логирование активируются флагами `observability.tracingEnabled`, `observability.metricsEnabled`, `observability.loggingEnabled`; при выключении соответствующие провайдеры и middleware не поднимаются ни для gRPC сервера, ни для воркера.
