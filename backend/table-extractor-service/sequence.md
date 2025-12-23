# Table Extractor Service — Sequence diagrams

Диаграммы отражают фактическое поведение gRPC-обработчиков, очереди в PostgreSQL и воркеров Table Extractor Service. Очередь хранится в таблице `table_extraction_queue`, обработка выполняется асинхронно воркерами, которые запускаются вместе с gRPC-сервером.

---

## 1) ExtractTablesFromPdf — постановка задачи и обработка

```mermaid
sequenceDiagram
    participant CL as gRPC клиент (gateway/worker)
    participant TE as TableExtractorService
    participant PG as Queue (table_extraction_queue)
    participant WK as Worker (ProcessExtractionJob)
    participant ST as Storage/Cache
    participant EX as Extractor (pdfinfo/pdftotext/pdftoppm)

    CL->>TE: ExtractTablesFromPdf(document_id,<br/>source_url | local_path | original_bucket/object_key,<br/>artifacts_bucket?, artifacts_prefix?, markdown_bucket/object_key?, correlation_id?)
    TE-->>TE: correlation_id = из метаданных или EnsureCorrelationID()
    TE-->>TE: Validate(document_id, source*, markdown_bucket/object_key парой,<br/>extension=pdf)
    TE-->>TE: Resolve artifacts_bucket (конфиг по умолчанию) и artifacts_prefix (template/default)
    TE->>PG: UpsertOrGetActive(document_id,<br/>queue_limit, active=[pending, processing])
    alt активная задача уже существует
        TE-->>CL: вернуть существующий job (status pending/processing)
    else новая постановка
        TE-->>CL: вернуть новый job (status=pending, attempts=0)
    end

    Note over WK,PG: Runner стартует воркеры параллельно gRPC-серверу
    loop worker_loop (NextRunAt <= now)
        WK->>PG: SelectPendingBatchForUpdate(limit)
        alt есть pending задачи
            WK-->>WK: перебор batch
            WK-->>PG: Save(status=processing, attempts++)
            alt источник не задан
                WK-->>PG: Save(status=failed, code=invalid_original,<br/>finished_at=now)
            else
                WK->>ST: Fetch файл (MinIO | HTTP | локальный путь)
                alt ошибка загрузки
                    WK-->>PG: Save(status=failed, code=download_error,<br/>finished_at=now)
                else
                    WK->>EX: Extract(pdfinfo + pdftotext + pdftoppm + parse)
                    alt ошибка извлечения
                        WK-->>PG: Save(status=failed, code=extraction_error,<br/>finished_at=now)
                    else
                        WK->>ST: Upload raw.json, normalized.json, manifest, debug/*
                        WK->>ST: Upload markdown (markdown_bucket/object_key или tables.md)
                        alt ошибка загрузки артефактов
                            WK-->>PG: Save(status=failed_retryable, code=upload_error)
                        else
                            WK-->>PG: Save(status=done, status_reason=completed,<br/>finished_at=now)
                        end
                    end
                end
            end
        else
            WK-->>WK: ждать backoff
        end
    end
```

---

## 2) Maintenance — восстановление, ретраи и очистка

```mermaid
sequenceDiagram
    participant MT as Maintenance loop
    participant PG as PostgreSQL Queue

    Note over MT: Отдельная горутина Runner; при старте вызывается RestoreStuckProcessing

    loop maintenance_interval
        MT->>PG: ListProcessingBefore(now - processing_timeout)
        alt attempts >= max_attempts
            MT->>PG: Save(status=failed, error_code=processing_timeout,<br/>finished_at=now)
        else требуется повтор
            MT->>PG: Save(status=pending, status_reason=restored_after_timeout,<br/>next_run_at=now+backoff(attempts))
        end

        MT->>PG: RetryFailed(status in [failed, failed_retryable],<br/>attempts < max_attempts?)
        MT-->>PG: Save(status=pending, next_run_at=now+retry_delay)

        MT->>PG: Cleanup(status in [done, failed],<br/>finished_at <= now-retention)
    end
```

---

## 3) Polling статуса — клиент GetTableExtractionJob

```mermaid
sequenceDiagram
    participant CL as Client (gateway/document-service)
    participant TE as TableExtractor gRPC
    participant PG as PostgreSQL Queue

    loop пока нужен статус
        CL->>TE: GetTableExtractionJob(queue_id, correlation_id?)
        TE-->>TE: EnsureCorrelationID() + SetHeader(correlation_id)
        TE->>PG: GetByID(queue_id)
        alt найден
            TE-->>CL: job(status, status_reason, artifacts, correlation_id)
            alt status in [pending, processing, failed_retryable]
                CL-->>CL: ждать/backoff и повторить
            else status in [done, failed]
                CL-->>CL: загрузить артефакты из MinIO
            end
        else
            TE-->>CL: NotFound
            break
        end
    end
```

---

## 4) Интеграция с пайплайном (Docling → TableExtractor → Merge)

```mermaid
sequenceDiagram
    participant SW as StageWorker / Workflow
    participant TR as Docling Transformer (gRPC)
    participant TE as TableExtractor (gRPC)
    participant MN as MinIO

    SW->>TR: ConvertDocumentToMarkdown(wait_for_result=false)
    TR-->>SW: queue_id/status + markdown_bucket/object_key

    SW->>TE: ExtractTablesFromPdf(document_id,<br/>original_bucket/object_key или source_url,<br/>artifacts_bucket?, markdown_bucket/object_key?)
    TE-->>SW: TableExtractionJob(queue_id/status,<br/>artifacts: raw/normalized/markdown/manifest/debug_prefix)

    loop poll (workflow backoff)
        SW->>TR: GetQueueStatus(queue_id)
        SW->>TE: GetTableExtractionJob(table_queue_id)
        alt оба статуса = done
            SW->>MN: GET markdown (docling)
            SW->>MN: GET tables.md (job.artifacts.markdown)
            SW-->>SW: Merge markdown + tables (опционально)
            SW->>MN: PUT финальный markdown (overwrite или новый key)
        else
            SW-->>SW: отложить повтор
        end
    end
```
