# Infrastructure view

```mermaid
graph LR
    Client[[Cron / gRPC клиент]] -->|создание задания| API[HTTP / gRPC слои]

    subgraph Preprocess Service
        API --> Queue[(PostgreSQL\npreprocess_queue)]
        Worker[Воркер LibreOffice] -->|poll + update\nstatus/next_run_at| Queue
    end

    Worker -->|S3 API: исходный объект| Src[(MinIO bucket source)]
    Worker -->|S3 API: результат| Tgt[(MinIO bucket preprocess-artifacts)]
    Worker --> Libre[LibreOffice контейнер]
    Worker -.->|callbacks (HTTP)| CB[Внешний callback]
```

- Таблица `preprocess_queue` в PostgreSQL хранит задания с idempotency-ключом `(source_bucket, source_key, source_etag)`, статусом,
  расписанием `next_run_at` и служебными таймштампами (`started_at`, `finished_at`).
- Индекс `(status, next_run_at)` ускоряет выборку задач воркером по расписанию, `(source_bucket, source_key)` и уникальный индекс по
  `(source_bucket, source_key, source_etag)` защищают от повторной постановки одного и того же объекта.
- Воркер забирает исходные файлы из MinIO, конвертирует их в LibreOffice и складывает результат в целевой bucket; при необходимости
  уведомляет внешние системы по `callback_url`.
