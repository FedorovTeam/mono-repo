# Table Extractor Service — Sequence diagrams

Диаграммы отражают асинхронную постановку задачи через gRPC и обработку через очередь в PostgreSQL воркерами с использованием poppler и сохранением результатов в MinIO.

---

## 1) ExtractTablesFromPdf → enqueue → worker → MinIO → GetJob

```mermaid
sequenceDiagram
    participant CL as Gateway/StageWorker (gRPC client)
    participant TE as TableExtractor gRPC
    participant PG as PostgreSQL Queue
    participant WK as Worker/Maintenance
    participant MN as MinIO
    participant PP as Poppler (pdfinfo/pdftotext)

    CL->>TE: ExtractTablesFromPdf(document_id, original_bucket, original_object_key,<br/>artifacts_bucket?, artifacts_prefix?, options?, wait_for_result?)
    TE-->>TE: correlation_id из gRPC metadata или EnsureCorrelationID()
    TE-->>TE: Validate(document_id, original_*, extension=pdf, artifacts_bucket resolved, queue_limit)
    TE->>PG: GetLatestByDocument(document_id)
    alt найдена последняя job неfailed
        TE-->>CL: reuse queue_id/status + artifacts_bucket/prefix + correlation_id
    else новая job
        TE->>PG: Enqueue(status=pending, original_bucket/key,<br/>artifacts_bucket/prefix, options, correlation_id)
        TE-->>CL: queue_id, status=pending, artifacts_bucket/prefix, correlation_id
    end

    opt wait_for_result=true
        loop WaitForResult (status_backoff + job.next_poll_at)
            TE->>PG: GetByID(queue_id)
            alt status in [done, failed]
                TE-->>CL: final status + artifacts_bucket/prefix + error?
            else
                TE-->>TE: sleep until next_poll_at/backoff
            end
        end
        TE-->>CL: DeadlineExceeded (table extraction result not ready)
    end

    loop workerLoop (MaxWorkers)
        WK->>PG: AcquireOne(pending where next_poll_at<=now FOR UPDATE SKIP LOCKED)
        alt job acquired
            WK->>PG: MarkProcessing(locked_by, locked_at, attempt++)
            WK->>MN: GET original_bucket/original_object_key
            WK->>PP: pdfinfo(file)
            alt pdf protected/broken
                WK->>PG: MarkFailed(status=failed, error="pdf not supported/protected/broken")
            else ok
                WK->>PP: pdftotext -layout (or pipeline)
                WK-->>WK: ParseTables + Normalize + Render (JSON/MD) + Manifest
                WK->>MN: PUT artifacts_prefix/tables.raw.json
                WK->>MN: PUT artifacts_prefix/tables.normalized.json
                WK->>MN: PUT artifacts_prefix/tables.md
                WK->>MN: PUT artifacts_prefix/manifest.json
                WK->>PG: MarkDone(status=done, finished_at, result_meta)
            end
        else no jobs
            WK-->>WK: sleep pollInterval
        end
    end
```

Maintenance loop
```mermaid
sequenceDiagram
    participant MT as Maintenance
    participant PG as PostgreSQL Queue

    loop maintenanceLoop (every N seconds)
        MT->>PG: CountByStatus() // metrics
        MT->>PG: RestoreStuckProcessing(processing_timeout, max_processing_retries)
        MT->>PG: RetryFailed(olderThan=now-failed_retry_delay, max_failed_retries)
        MT->>PG: CleanupFinished(retention)
    end

```

Интеграция с пайплайном (Docling → TableExtractor → Merge)
```mermaid
sequenceDiagram
    participant SW as StageWorker / Workflow
    participant TR as Docling Transformer (gRPC)
    participant TE as TableExtractor (gRPC)
    participant PG as PostgreSQL
    participant MN as MinIO

    SW->>TR: ConvertDocumentToMarkdown(wait_for_result=false)
    TR-->>SW: queue_id/status + markdown_bucket/object_key

    SW->>TE: ExtractTablesFromPdf(document_id, original_bucket/object_key, wait_for_result=false)
    TE-->>SW: table_queue_id/status + artifacts_bucket/prefix

    loop poll (workflow backoff)
        SW->>TR: GetQueueStatus(queue_id) or GetByID(queue_id)
        SW->>TE: GetTableExtractionJob(table_queue_id)
        alt both ready
            SW->>MN: GET markdown (docling)
            SW->>MN: GET tables.md (poppler)
            SW-->>SW: Merge markdown + tables (optional)
            SW->>MN: PUT final markdown (overwrite or new key)
        else
            SW-->>SW: reschedule next_run_at/backoff
        end
    end

```