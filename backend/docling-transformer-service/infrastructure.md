# Infrastructure view

```mermaid
graph LR
    GW[Gateway / Workflows clients] -->|gRPC :service.grpc_port (9090 по умолчанию)| API[Transformer gRPC API]

    subgraph App
        API -->|enqueue / restore| PG[(PostgreSQL\ntransformations_queue)]
        WRK[Queue worker\n(MaxWorkers)] -->|polls + updates| PG
    end

    WRK -->|S3 API download/upload| MN[(MinIO buckets: original* / markdown_bucket)]
    WRK -->|HTTP POST /v1/convert/file/async\nX-Correlation-Id, X-Docling-Source-Filename, X-Docling-Endpoint, X-Api-Key?\nmultipart files| DL[Docling Serve]
    WRK -->|HTTP GET /v1/status/poll/{job_id}\nHTTP GET /v1/result/{job_id}| DL

    API -.->|OTLP (traces/metrics/logs)\nкогда включено| OBS[OTel Collector]
    WRK -.->|OTLP (traces/metrics/logs)\nкогда включено| OBS
```

- gRPC сервер слушает порт из `service.grpc_port` (в `config/config.yaml` по умолчанию `9090`) и принимает запрос `ConvertDocumentToMarkdown`, который ставит задачу в таблицу `transformations_queue` PostgreSQL. Контроль параллельности выполняется воркером через `service.max_workers`, а приём новых задач ограничивается отдельным лимитом `service.queue_limit` (если задан).
- Воркер (`internal/worker`) периодически забирает задания из очереди, скачивает исходный файл из MinIO (bucket и key приходят в gRPC-запросе), отправляет документ в Docling асинхронно и обновляет очередь после получения результата.
- Docling вызывается по HTTP `POST /v1/convert/file/async` с формой `files`, прокидыванием `X-Correlation-Id`, исходного имени файла (`X-Docling-Source-Filename`), выбранного эндпоинта (`X-Docling-Endpoint`) и при необходимости `X-Api-Key`. Результат запрашивается через `/v1/status/poll/{job_id}` и `/v1/result/{job_id}`.
- Markdown сохраняется в MinIO в `markdown_bucket` (по умолчанию `minio.bucket` из конфигурации); ключ результата строится в gRPC слое, если не передан явно.
- Параметры Temporal присутствуют в конфигурации для клиента, но сервис пока не устанавливает подключение.
- Трассировка, метрики и логирование активируются флагами `observability.tracingEnabled`, `observability.metricsEnabled`, `observability.loggingEnabled`; при выключении соответствующие провайдеры и middleware не поднимаются ни для gRPC сервера, ни для воркера.
