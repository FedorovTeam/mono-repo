# Infrastructure view

```mermaid
graph LR
    GW[Gateway / Workflows clients] -->|gRPC :service.grpc_port (9090 по умолчанию)| API[Transformer gRPC API]

    subgraph App
        API -->|ConvertDocumentToMarkdown\nenqueue / restore / optional wait_for_result| PG[(PostgreSQL\ntransformations_queue)]
        WRK[Queue worker + maintenance\n(MaxWorkers)] -->|polls + updates| PG
    end

    WRK -->|S3 API download/upload\noriginal / source / markdown| MN[(MinIO buckets)]
    WRK -->|HTTP POST /v1/convert/file/async\nили /v1/convert/source/async (source_async)\nHTTP GET /v1/status/poll/{job_id}\nHTTP GET /v1/result/{job_id}| DL[Docling Serve]

    API -->|ConvertDocxToMarkdown\n(docx_content или docx_url)\nDocling sync convert| DL

    API -.->|OTLP (traces/metrics/logs)\nкогда включено| OBS[OTel Collector]
    WRK -.->|OTLP (traces/metrics/logs)\nкогда включено| OBS
```

- gRPC сервер слушает порт из `service.grpc_port` (в `config/config.yaml` по умолчанию `9090`) и принимает:
  - `ConvertDocumentToMarkdown` — ставит задачу в таблицу `transformations_queue` PostgreSQL. Контроль параллельности выполняется воркером через `service.max_workers`, а приём новых задач ограничивается отдельным лимитом `service.queue_limit` (если задан). При `wait_for_result` сервер блокируется до готовности задачи, опрашивая очередь по backoff-политике `docling.*`.
  - `ConvertDocxToMarkdown` — синхронно проксирует docx-файл (или скачивает `docx_url`) в Docling и возвращает готовый Markdown без постановки в очередь и без MinIO.
- Воркер (QueueWorker/Processor) периодически забирает задания из очереди, восстанавливает их состояние при рестарте (`RestoreProcessing`), скачивает исходный файл из MinIO или по `source_url`, либо в `source_async` режиме передаёт Docling ссылки на бакет/объект. Markdown сохраняется в MinIO (`markdown_bucket` из конфигурации, либо из gRPC запроса). Ключ результата строится в gRPC слое, если не передан явно.
- Docling вызывается по HTTP `POST /v1/convert/file/async` с формой `files` либо `POST /v1/convert/source/async`, куда пробрасываются `X-Correlation-Id`, исходное имя файла (`X-Docling-Source-Filename`), выбранный эндпоинт (`X-Docling-Endpoint`), `X-Api-Key` (если нужен) и параметры OCR/PDF backend. Результат запрашивается через `/v1/status/poll/{job_id}` и `/v1/result/{job_id}`.
- Для PDF с `force_ocr=true` используется двухфазный overlay: базовый Markdown сохраняется как `<key>.base.md`, затем OCR-проход накладывает таблицы и перезаписывает финальный `markdown_object_key`.
- Планировщик обслуживания (`MaintenanceRunner`) следит за зависшими задачами (time-out на обработку и Docling), переводит их обратно в очередь/ошибку, повторяет попытки для `failed` по задержке и периодически чистит устаревшие выполненные задачи (`cleanup_retention`).
- Вызовы приходят от cron-воркеров `document-service`, которые по статусам в PostgreSQL решают, какой документ нужно конвертировать; сам сервис не хранит состояния workflow.
- Трассировка, метрики и логирование активируются флагами `observability.tracingEnabled`, `observability.metricsEnabled`, `observability.loggingEnabled`; при выключении соответствующие провайдеры и middleware не поднимаются ни для gRPC сервера, ни для воркера.
