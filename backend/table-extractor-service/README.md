# Table Extractor Service

Table Extractor Service — gRPC-сервис для **детерминированного** извлечения таблиц из **PDF** (в т.ч. “таблиц голосования/протоколов”) с помощью **poppler** и Go-пайплайна. Запросы ставятся в очередь в PostgreSQL и обрабатываются воркерами, чтобы держать предсказуемую нагрузку на CPU/IO и MinIO. Сервис прокидывает `x-correlation-id` во все внешние операции и конфигурируется через один `config.yaml` (в стиле остальных сервисов mono-repo).

## Возможности

- gRPC метод `ExtractTablesFromPdf` (контракт `proto/backend/tableextractor/v1/table_extractor.proto`) принимает `document_id` и путь к исходному PDF (`original_bucket`/`original_object_key`), ставит задачу в очередь `table_extraction_queue` и возвращает `queue_id`, `status=pending`, путь артефактов и `correlation_id`.
- Идемпотентность по `document_id`: повторный вызов возвращает существующую **последнюю** задачу не в статусе `failed` без создания дублей и возвращает исходный `correlation_id`.
- Проверка переполнения очереди (`service.queue_limit`): `ResourceExhausted`, если `pending/processing` достигли лимита; `service.max_workers` ограничивает только параллелизм фоновых воркеров.
- Воркер в фоне:
    - скачивает PDF из MinIO (или из `source_url`, если включено),
    - выполняет проверки через `pdfinfo`,
    - извлекает табличный текст/структуру через poppler (`pdftotext -layout` или другой режим),
    - парсит/нормализует таблицы,
    - сохраняет результаты в MinIO и помечает задачу как `done/failed`.
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
- `minio.*` — endpoint/creds для MinIO + `artifacts_bucket` и `default_artifacts_prefix` по умолчанию.
- `database.driver` / `database.dsn` — `postgres` или `sqlite`, DSN строки и параметры пула.
- `storage.*` — таймауты HTTP-клиента к MinIO (`dial_timeout`, `read_write_timeout`, `operation_timeout`).
- `extractor.mode` — режим извлечения (`layout`/`structured`), `extractor.detect_voting_tables` — включает детектор протоколов.
- `extractor.pages` — диапазон страниц (`all`, `1-3`, `odd`, т.д. — в зависимости от реализации пайплайна).

## Интеграции и зависимости

- **PostgreSQL**: таблица `table_extraction_queue` хранит заявки и статусы.
- **MinIO**: источник PDF и место хранения артефактов (JSON/Markdown/manifest).
- **Poppler**: `pdfinfo` и `pdftotext` (или другие утилиты poppler, которые использует твой пайплайн). Должны быть доступны внутри контейнера/окружения.

## gRPC API (кратко)

- `ExtractTablesFromPdf(...)` — поставить задачу в очередь (опционально `wait_for_result`).
- `GetTableExtractionJob(queue_id)` — получить статус и пути артефактов.
- `ListTableExtractionJobs(...)` (опционально) — список задач по фильтрам.

`x-correlation-id` читается из gRPC metadata (или генерируется), возвращается в ответе и прокидывается в логи/внешние операции.

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
- `internal/infrastructure/storage` — клиент MinIO (download/upload), прокидывание `correlation_id`.
- `internal/infrastructure/telemetry` — утилиты correlation-id и лог-контекст.
- `proto/backend/tableextractor/v1` — gRPC контракт и сгенерированные артефакты.
- `db/migrations/` — SQL миграции очереди.

## Логи и наблюдаемость

- Каждый gRPC вызов логируется с `correlation_id`.
- Воркер логирует жизненный цикл задачи: `acquired → processing → done/failed`, включая `attempt`, длительности этапов (download/extract/upload) и причины ошибок.
- Метрики (если включены в `observability.*`): gauge по статусам очереди, счётчики `tasks_completed/tasks_failed`, гистограммы длительности обработки и размеров таблиц/страниц (если есть).
