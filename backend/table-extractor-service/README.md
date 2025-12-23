# Table Extractor Service

Table Extractor Service — gRPC-сервис для **детерминированного** извлечения таблиц из **PDF** (в т.ч. “таблиц голосования/протоколов”) с помощью **poppler** и Go-пайплайна. Запросы ставятся в очередь в PostgreSQL и обрабатываются воркерами, чтобы держать предсказуемую нагрузку на CPU/IO и MinIO. Сервис прокидывает `x-correlation-id` во все внешние операции и конфигурируется через один `config.yaml` (в стиле остальных сервисов mono-repo).

## Возможности

- gRPC метод `ExtractTablesFromPdf` (контракт `proto/backend/tableextractor/v1/table_extractor.proto`) принимает `document_id` и путь к исходному PDF (`original_bucket`/`original_object_key`), ставит задачу в очередь `table_extraction_queue` и возвращает `queue_id`, `status=pending`, путь артефактов и `correlation_id`. Для Markdown можно передать отдельный bucket/key (`markdown_bucket`/`markdown_object_key`), чтобы складывать итоговые таблицы отдельно от остальных артефактов.
- Идемпотентность по `document_id`: повторный вызов возвращает существующую **последнюю** задачу не в статусе `failed` без создания дублей и возвращает исходный `correlation_id`.
- Проверка переполнения очереди (`service.queue_limit`): `ResourceExhausted`, если `pending/processing` достигли лимита; `service.max_workers` ограничивает только параллелизм фоновых воркеров.
- Воркер в фоне:
    - скачивает PDF из MinIO (или из `source_url`, если включено),
    - выполняет проверки через `pdfinfo`,
    - извлекает табличный текст/структуру через poppler (`pdftotext -layout` или `-bbox` для протоколов),
    - парсит/нормализует таблицы,
    - сохраняет результаты в MinIO и помечает задачу как `done/failed`.
    - при включённом `extractor.detect_voting_tables` — берёт координаты колонок из `pdftotext -bbox`, сканирует PNG от `pdftoppm` и проставляет ✓ в нужные ячейки перед генерацией Markdown.
- Артефакты сохраняются “пачкой” в один prefix:
    - `tables.raw.json` (сырой результат),
    - `tables.normalized.json`,
    - `tables.md`,
    - `manifest.json` (версия алгоритма/параметры/метрики/сводка),
    - опционально `debug/*`.
- При старте сервис запускает maintenance-цикл очереди:
    - восстанавливает `processing` → `pending` после рестарта,
    - возвращает зависшие `processing` по `service.processing_timeout`,
    - по правилам `scheduler.*` повторяет `failed_retryable` (если включён),
    - чистит завершённые задачи по `cleanup_retention` (опционально).

### Политика retry/timeout

- gRPC-слой не дублирует задачи: если запись уже есть для `document_id`, возвращает её `queue_id` и `status`.
- Лимит активных задач — `service.queue_limit` (учитываются `pending/processing`).
- Воркер использует `scheduler.retry_backoff`, `scheduler.max_processing_retries`, `service.processing_timeout` и `scheduler.failed_retry_delay/max_failed_retries` для повторов и восстановления зависших задач.
- Ошибки формата (битый/защищённый PDF, неподдерживаемое расширение) завершаются `failed` без повторов; временные ошибки IO/MinIO могут уходить в `failed_retryable` или возвращаться в `pending` с `next_poll_at`.

## Конфигурация

Все параметры в `config/config.yaml`, путь задаётся `CONFIG_PATH` (по умолчанию `config/config.yaml`). Ключевые поля:

- `service.name` / `service.port` — имя сервиса и порт gRPC (обычно `9090`).
- `service.queue_limit` — лимит активных задач (`pending/processing`), `0` отключает лимит.
- `service.max_workers` — число параллельных воркеров очереди.
- `service.processing_timeout` — TTL обработки одной задачи (используется maintenance-циклом).
- `service.retry.*` — политика повторов (кол-во попыток, `backoff_interval`, `max_backoff`).
- `service.cleanup_retention` — TTL для `done/failed` записей (удаление/архив).
- `minio.*` — endpoint/creds для MinIO + `artifacts_bucket`, `default_artifacts_prefix` по умолчанию и `artifacts_prefix_template` (`{{ .DefaultPrefix }}/{{ .DocumentID }}/{{ .QueueID }}` по умолчанию).
- `database.driver` / `database.dsn` — `postgres` или `sqlite`, DSN строки и параметры пула.
- `storage.*` — таймауты HTTP-клиента к MinIO (`dial_timeout`, `read_write_timeout`, `operation_timeout`).
- `extractor.mode` — режим извлечения для poppler (`layout` / `bbox`); от него зависит аргумент `pdftotext` (`-layout` или `-bbox`). `extractor.detect_voting_tables` — включает детектор протоколов и принудительно переводит `pdftotext` в `-bbox`, чтобы собрать координаты колонок/галочек.
- `extractor.pdfinfo_path` / `extractor.pdftotext_path` / `extractor.pdftoppm_path` — явные пути к бинарям poppler (по умолчанию используются команды из `$PATH`).
- `extractor.pdftoppm_dpi` — DPI рендера PNG для поиска галочек (по умолчанию 200).
- `extractor.pages` — диапазон страниц (`all`, `1-3`, `odd`, т.д. — в зависимости от реализации пайплайна).
- `observability.logging_enabled` / `observability.log_level` / `observability.tracing.enabled` — включение логов, уровень (`debug|info|warn|error`, по умолчанию `info`) и трассировка; логи пишутся в JSON-формате.

## Интеграции и зависимости

- **PostgreSQL**: таблица `table_extraction_queue` хранит заявки и статусы.
- **MinIO**: источник PDF и место хранения артефактов (JSON/Markdown/manifest).
- **Poppler**: `pdfinfo`, `pdftotext` (для `-layout/-bbox`) и `pdftoppm` для рендера PNG (галочки читаются из картинок). Требуется попплер с поддержкой `-bbox`; рекомендуется держать DPI рендера не ниже 150–200 для устойчивого распознавания ✓.

## gRPC API

### TableExtractorService

- **ExtractTablesFromPdf** — постановка задачи в очередь.
  - Обязательные поля: `document_id`, `original_bucket`, `original_object_key` (или `source_url` вместо пары bucket/object).
  - Опциональные поля: `artifacts_bucket` (по умолчанию `minio.artifacts_bucket`), `artifacts_prefix` (по умолчанию генерируется из `minio.artifacts_prefix_template` или `minio.default_artifacts_prefix/<document_id>/<queue_id>`), `markdown_bucket`/`markdown_object_key` (для сохранения `tables.md` в отдельное место), `correlation_id` (перезаписывает сгенерированный), `wait_for_result` (пока не используется — метод всегда возвращает сразу после enqueue).
  - Ответ: `job.queue_id`, `status` (`pending|processing|done|failed|failed_retryable`), итоговые bucket/prefix, `artifacts` (с учётом кастомного пути для Markdown), `correlation_id`.
- **GetTableExtractionJob** — получение статуса по `queue_id`.
  - Возвращает статус, `attempts`, `status_reason/error_code`, `artifacts` с готовыми путями, `created_at/updated_at`, `correlation_id`.

`x-correlation-id` читается из gRPC metadata (или генерируется в хэндлере), возвращается в ответе и прокидывается в логи/MinIO запросы. Идемпотентность обеспечивается уникальным индексом по `document_id` и методом репозитория `UpsertOrGetActive`.

## Очередь и статусы

- Основная таблица: `table_extraction_queue` (PostgreSQL/SQLite), миграции лежат в `db/migrations`.
- Статусы: `pending → processing → done/failed/failed_retryable`.
- Поля: `queue_id` (PK), `document_id`, `original_bucket/key`, `artifacts_bucket/prefix`, `markdown_bucket/object_key` (опционально), `status`, `attempts`, `error`, `status_reason`, `next_run_at`, `created_at/updated_at/finished_at`, `correlation_id`.
- Контроль нагрузки:
  - `service.queue_limit` — верхняя граница активных задач (`pending/processing`); при превышении вернётся `ResourceExhausted`.
  - `service.batch_size` — размер выборки задач за проход воркера, `service.max_workers` — параллелизм.
  - `service.retry.*` + `processing_timeout` управляют восстановлением зависших задач и бэк‑оффами.
- Диагностика: `status_reason`, `attempts`, `next_run_at`, `error_code/error_message`, `correlation_id`.
- Идемпотентность: уникальный индекс `uq_table_extraction_queue_active_document` по `document_id` для статусов `pending/processing`.

## Формат артефактов (MinIO)

Рекомендуемый prefix:

`tables/<document_id>/<queue_id>/`

Файлы:

- `tables.raw.json`
- `tables.normalized.json`
- `tables.md`
- `manifest.json`
- `debug/*` (опционально)

`manifest.json` содержит:

- версии алгоритма/бинарника,
- poppler version,
- параметры извлечения,
- сводку: страницы, кол-во таблиц, метрики качества, время выполнения.

## Структура проекта

- `cmd/main.go` — точка входа: загрузка конфига, миграции, запуск gRPC и воркеров очереди.
- `internal/config` — загрузка `config.yaml`.
- `internal/server` — gRPC сервер, интерсепторы (логирование/корреляция), постановка задач в очередь.
- `internal/repository` — доступ к `table_extraction_queue` (enqueue, acquire через `FOR UPDATE SKIP LOCKED`, обновление статусов).
- `internal/worker` — воркеры извлечения + maintenance цикл.
- `internal/extractor` — пайплайн poppler: вызовы `pdfinfo/pdftotext`, парсинг таблиц, нормализация, рендер (JSON/MD), manifest.
- `internal/storage` — клиент MinIO (download/upload), прокидывание `correlation_id`.
- `internal/telemetry` — утилиты correlation-id, логгер и tracer.
- `proto/backend/tableextractor/v1` — gRPC контракт и сгенерированные артефакты.
- `db/migrations/` — SQL миграции очереди.

## Логи и наблюдаемость

- Каждый gRPC вызов логируется с `correlation_id`.
- Воркер логирует жизненный цикл задачи: `acquired → processing → done/failed`, включая `attempt`, длительности этапов (download/extract/upload) и причины ошибок.
- Метрики (если включены в `observability.*`): gauge по статусам очереди, счётчики `tasks_completed/tasks_failed`, гистограммы длительности обработки и размеров таблиц/страниц (если есть).
  - Трассировки включаются через `observability.tracing.enabled`; gRPC сервер использует `otelgrpc` stats handler, провайдер инициализируется в `internal/telemetry`.
  - Подключение метрик можно расширить через `go.opentelemetry.io/otel/metric` (зависимость уже добавлена).
- Уровень логирования задаётся через `observability.log_level` (значения: `debug`, `info`, `warn`, `error`) и применяется на старте сервиса; формат логов — структурированный JSON в stdout/stderr от `zap`.

## Локальный прогон (MinIO + gRPC)

1. Поднимите MinIO:  
   `docker run --rm -it -p 9000:9000 -p 9001:9001 -e MINIO_ROOT_USER=minioadmin -e MINIO_ROOT_PASSWORD=minioadmin minio/minio:RELEASE.2024-11-07T23-46-52Z server /data --console-address ":9001"`  
   Создайте бакеты `table-extractor-originals` и `table-extractor-artifacts`:  
   `mc alias set local http://localhost:9000 minioadmin minioadmin --api S3v4 && mc mb local/table-extractor-originals && mc mb local/table-extractor-artifacts`.
2. Скопируйте `config/config.yaml.example` → `config/config.yaml`, пропишите MinIO endpoint/ключи, при необходимости DSN Postgres. Миграции применяются автоматически при запуске сервиса.
3. Запустите сервис: `CONFIG_PATH=config/config.yaml go run ./cmd/main.go`.
4. Отправьте задачу через gRPC (пример для файла в MinIO):
   ```bash
   grpcurl -plaintext -d '{
     "documentId": "doc-manual",
     "originalBucket": "table-extractor-originals",
     "originalObjectKey": "input/document.pdf",
     "artifactsBucket": "table-extractor-artifacts",
     "artifactsPrefix": "tables/doc-manual"
   }' localhost:9090 backend.tableextractor.v1.TableExtractorService/ExtractTablesFromPdf
   ```
   Полученный `queueId` используйте в `GetTableExtractionJob`:
   `grpcurl -plaintext -d '{"queueId":"<queueId>"}' localhost:9090 backend.tableextractor.v1.TableExtractorService/GetTableExtractionJob`.
5. Проверьте артефакты в MinIO: `mc ls local/table-extractor-artifacts/tables/doc-manual/` и содержимое `tables.md`/`manifest.json`. Для быстрого сравнения можно использовать эталонные файлы в `fixtures/regular/` (PDF нужно сгенерировать из `document.base64`: `base64 -d fixtures/regular/document.base64 > /tmp/regular.pdf`). Подробный чек-лист — в `test.md`.

### Быстрые сценарии ручного тестирования

1. **Постановка задачи** — вызвать `ExtractTablesFromPdf` (пример выше), убедиться, что вернулся `status=pending` и `correlation_id`.
2. **Polling статуса** — повторно опросить `GetTableExtractionJob` и отследить переходы `pending → processing → done/failed`.
3. **Проверка артефактов** — скачать `tables.md` и `manifest.json` из MinIO и сверить с эталонами `fixtures/*`.
4. **Проверка лимитов** — установить `service.queue_limit=1`, отправить две заявки подряд: вторая должна завершиться `ResourceExhausted`.
5. **Восстановление зависших задач** — временно уменьшить `processing_timeout`, запустить задачу и убедиться, что maintenance-цикл переводит её обратно в `pending` или помечает `failed`.

## Code view

Актуальная диаграмма модулей находится в [`code.md`](code.md) и соответствует текущей реализации сервиса.
