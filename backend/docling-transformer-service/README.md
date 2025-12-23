# Docling Transformer Service

gRPC-обёртка над Docling, которая ставит конвертацию DOC/DOCX/PDF → Markdown в очередь PostgreSQL и обрабатывает её фоновым воркером. В конфигурации один `config.yaml`, все вызовы прокидывают/генерируют `x-correlation-id`, миграции БД прогоняются автоматически при старте. 

## gRPC API
- `ConvertDocumentToMarkdown` (`proto/backend/transformer/v1/transformer.proto`): принимает `document_id`, `original_bucket/object_key`, опционально `markdown_bucket/object_key`, `filename_extension`, источник (`source_bucket/object_key` **или** `source_url`), `wait_for_result`, `correlation_id`. Ставит запись в `transformations_queue`, возвращает `queue_id`, статус и фактические путь/корреляцию. Повторный вызов с тем же `document_id` и незавершённой задачей возвращает существующую запись без дублей. При `wait_for_result=true` опрашивает очередь по backoff `docling.status_*` и `next_poll_at` до `done/failed`.
- `ConvertDocxToMarkdown`: принимает либо `docx_content`, либо `docx_url` (оба сразу запрещены), опциональный `filename` и `correlation_id`; скачивает файл по URL при необходимости, отправляет напрямую в Docling и возвращает Markdown/медиатип/charset без постановки в очередь.

## Очередь и обработка
- Таблица `transformations_queue` создаётся миграциями при старте; ключевые поля: `status` (`pending`/`processing`/`waiting_docling`/`done`/`failed`), `document_id`, `original_bucket/object_key`, `markdown_bucket/object_key`, `filename_extension`, `source_bucket/object_key/url`, счётчики `retry_count`/`docling_poll_count`, `docling_task_id`/`docling_job_id`, `next_poll_at`, таймстемпы и индексы по статусам/дате/Docling ID/`next_poll_at`.
- Постановка задачи: валидация обязательных bucket/key, взаимоисключение `source_url` и `source_*`, нормализация расширения, вычисление `markdown_object_key` по умолчанию и проверка лимита очереди (`service.queue_limit` считает `pending`/`processing`/`waiting_docling`). Уже существующая неfailed‑задача по `document_id` переиспользуется.
- Выборка воркером (`QueueWorker`): приоритет `waiting_docling` с наступившим `next_poll_at`; наличие любых `waiting_docling` блокирует выдачу `pending`. Задачи берутся пачками `BatchSize` через `SELECT … FOR UPDATE SKIP LOCKED`, статусы переводятся в `processing`, retry счётчик увеличивается только для `pending`.
- Обработка (`ConversionExecutor`): подставляет `markdown_bucket/object_key`, определяет расширение/`Content-Type`, выбирает режим `source_async`, если включён и нет `source_url` (или переданы `source_bucket/object_key`). В `source_async` требуется bucket/key; иначе источник скачивается из MinIO/URL с быстрым таймаутом. Отправка в Docling сериализована advisory‑локом `pg_try_advisory_lock`; при занятости задача возвращается в `pending` с `next_poll_at=now+submit_lock_delay`. Успешный submit пишет Docling ID и `next_poll_at` по backoff. Результат Docling выгружается в MinIO, после чего статус меняется на `done`.
- Опрос Docling (`DoclingPoller`): каждый статус‑запрос увеличивает `docling_poll_count`, сохраняет Docling ID и метрики. Ошибки статуса и превышение `docling.status_max_attempts` приводят к `RequeueDoclingWaiting` — статус `pending`, сброс Docling ID/счётчиков, рост `retry_count` и задержка `scheduler.docling_requeue_delay` до следующей попытки. При готовности результат сразу забирается и передаётся на загрузку.
- Обслуживание очереди (`MaintenanceRunner`): периодически снимает метрики размеров очереди, помечает `waiting_docling` старше `docling.max_job_lifetime` как `failed`, восстанавливает зависшие `processing` (по `service.processing_timeout`, `scheduler.max_processing_retries`, `docling.status_max_attempts`), перекидывает `failed` старше `scheduler.failed_retry_delay` обратно в `pending` пока не превышен `scheduler.max_failed_retries`, удаляет завершённые задачи старше `scheduler.cleanup_retention`. При старте все `processing` переводятся в `pending`.
- `wait_for_result` на gRPC слое использует ту же backoff‑политику статусов, учитывает `next_poll_at` и возвращает `DeadlineExceeded`, если попытки исчерпаны.

## Интеграция с Docling и MinIO
- Эндпоинты Docling: `POST /v1/convert/file/async` (multipart `files`, `X-Correlation-Id`, `X-Docling-Source-Filename`, `X-Docling-Endpoint`, опционально `X-Api-Key`) и `POST /v1/convert/source/async` (JSON со ссылками на S3/MinIO и теми же заголовками). Результат опрашивается через `GET /v1/status/poll/{id}` и `GET /v1/result/{id}`. Submit ретраится до 5 раз с backoff 0.5s → 5s; временное истощение ретраев переводит задачу в `waiting_docling` для повторной отправки.
- Режим `source_async` требует `docling.storage.*` (подставляется из `minio.*`, если явно не задан) и передаёт Docling ссылку на исходный объект, опционально target bucket/key для записи результата. В обычном режиме файл скачивается из MinIO (повторные попытки при `NoSuchKey`) или по HTTP и загружается в Docling целиком. Markdown всегда складывается в MinIO.
- Docling запрашивается сразу в `md` и `json`, чтобы получать `json_content` с таблицами для автодетекции `table_metadata` и постановки задач `table-extractor`.

## Конфигурация
`CONFIG_PATH` указывает на YAML (по умолчанию `config/config.yaml`). Валидация требует непустых `service.name`, положительных портов/таймаутов, неотрицательных лимитов очереди/ретраев, DSN Postgres, полного блока MinIO и `docling.endpoint`. Поддерживаются только `docling.mode=upload_file|source_async`; для `source_async` необходимы `docling.storage.endpoint/access_key/secret_key/bucket`. `docling.storage` по умолчанию копирует `minio.*`. `docling_render.prefer_json` заполняется по умолчанию (true), но на построение результата сейчас не влияет.

Основные параметры:
- `service.*`: имя, `grpc_port`, параллелизм воркеров (`max_workers`), лимит очереди (`queue_limit`), таймауты обработки/IO.
- `scheduler.*`: интервалы опроса/обслуживания, размер батча, backoff для ошибок обработки/submit‑лока, лимиты/паузы ретраев `processing`/`failed` задач, параметры реqueue Docling (`docling_requeue_delay`; `docling_max_restarts` ограничивает число рестартов после потери задания Docling — при превышении задача завершается со статусом `failed` без нового submit).
- `docling.*`: endpoint/API key/язык, режим `mode`, статусный backoff (`status_initial_interval`, `status_backoff_factor`, `status_max_interval`, `status_max_attempts`), `max_job_lifetime`, настройки хранилища Docling.
- `minio.*`: endpoint, ключи, обязательный `bucket` для результата, `region`, `use_ssl`.
- `postgres.*`: `dsn`, лимит коннектов.
- `observability.*`: включение логов/метрик/трассировки.

## Локальный запуск
```bash
# применить миграции и выйти
go run ./cmd/main.go -config config/config.yaml -migrate-only

# запустить сервис (миграции применяются автоматически)
go run ./cmd/main.go -config config/config.yaml
```
Путь к конфигу можно задать флагом `-config` или переменной `CONFIG_PATH`. Логи пишутся в STDOUT, correlation-id берётся из метаданных gRPC или генерируется.

## Тестирование
```bash
go test ./...
```

## Наблюдаемость
- Флаги `observability.loggingEnabled|metricsEnabled|tracingEnabled` управляют инициализацией логгера и OTEL провайдеров; при отключении выставляются no-op реализации.
- Метрики воркера (OTEL): gauges `docling_transformer_queue_pending` / `docling_transformer_queue_processing` / `docling_transformer_queue_docling_waiting`; counters `docling_transformer_tasks_acquired` / `docling_transformer_tasks_completed` / `docling_transformer_tasks_failed` / `docling_transformer_tasks_restored` / `docling_transformer_tasks_rejected` / `docling_transformer_tasks_failed_rescheduled` / `docling_transformer_tasks_failed_exhausted` / `docling_transformer_docling_job_requeued` / `docling_transformer_docling_submitted`; histograms `docling_transformer_processing_duration_ms` / `docling_transformer_docling_wait_duration_ms` / `docling_transformer_docling_poll_attempts` / `docling_transformer_queue_batch_size`.

## Структура проекта
- `cmd/main.go` — загрузка конфигурации, миграции, инициализация MinIO/Postgres/Docling, запуск воркера и gRPC.
- `internal/interfaces/grpc` — gRPC сервер и валидация входа.
- `internal/usecase` — бизнес-логика постановки задач, ожидания результата, воркер, опрос Docling и обслуживание очереди.
- `internal/infrastructure/repository` — Postgres-очередь (`transformations_queue`), advisory lock, восстановление/очистка.
- `internal/infrastructure/converter` — HTTP-клиент Docling (submit/poll/result, заголовки, ретраи, режим `source_async`).
- `internal/infrastructure/storage` — адаптер MinIO для скачивания исходников и загрузки Markdown.
- `internal/config`, `internal/domain`, `internal/infrastructure/telemetry` — конфигурация, типы/утилиты, OTEL.
- `db/migrations` — миграции `transformations_queue` и индексов.
