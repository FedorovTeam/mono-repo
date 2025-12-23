# Table Extractor Service — Flows

Документ описывает основные потоки сервиса и ключевые параметры конфигурации для постановки задач извлечения таблиц и их обработки воркером.

## Поток: ExtractTablesFromPdf → очередь
1. gRPC вызов `ExtractTablesFromPdf(document_id, original_bucket?, original_object_key?, source_url?, artifacts_bucket?, artifacts_prefix?, markdown_bucket/object_key?, wait_for_result?, correlation_id?)`.
2. Валидация: `document_id` обязателен; нужен источник PDF — либо `source_url`, либо пара `original_bucket` + `original_object_key` (локальный путь поддерживается внутри use-case, но не через gRPC); для любого источника проверяется расширение `pdf`; `markdown_bucket` и `markdown_object_key` задаются только вместе; `correlation_id` генерируется сервером, если не передан.
3. `artifacts_bucket` берётся из запроса или `minio.artifacts_bucket`, иначе ошибка `invalid_argument`. `artifacts_prefix` — из запроса, либо результат `minio.artifacts_prefix_template` (`{{ .DefaultPrefix }}/{{ .DocumentID }}/{{ .QueueID }}`), либо fallback `minio.default_artifacts_prefix/<document_id>/<queue_id>`.
4. Создаётся `TableExtractionJob` в статусе `pending` с артефактами: `tables.raw.json`, `tables.normalized.json`, `manifest.json` и `debug/` в `artifacts_bucket`/`artifacts_prefix`; `tables.md` кладётся в `markdown_bucket/object_key`, либо по умолчанию в тот же `artifacts_bucket`/`artifacts_prefix`.
5. Постановка в `table_extraction_queue` через `UpsertOrGetActive`: idempotency по `document_id` для `pending/processing`, лимит активных задач (`service.queue_limit`) применяется только при создании новой записи. При переполнении — `ResourceExhausted`.
6. Ответ: `queue_id`, `status`, расположение артефактов и `correlation_id` (также в header). Поле `wait_for_result` сейчас не используется — обработка всегда асинхронная, ожидание статуса делается через отдельный polling.

## Поток: GetTableExtractionJob (polling)
1. gRPC вызов `GetTableExtractionJob(queue_id, correlation_id?)`.
2. Возврат состояния задачи (`pending/processing/done/failed/failed_retryable`) и путей артефактов; при неизвестном `queue_id` — `NotFound`. `correlation_id` резолвится аналогично `ExtractTablesFromPdf`.

## Поток: worker loop
1. `SelectPendingBatchForUpdate(... FOR UPDATE SKIP LOCKED)` до `service.batch_size` задач за тик; одновременно работают до `service.max_workers` воркеров.
2. Перевод в `processing`, инкремент `attempts`, сброс `next_run_at`.
3. Загрузка исходника: сначала `source_url` (HTTP/HTTPS или `file://`), затем `local_path` (если установлен), иначе скачивание из MinIO по `original_bucket/object_key` через `internal/storage` с пробросом `correlation_id`. Отсутствие источника или ошибка загрузки приводит к `failed` с `error_code` `invalid_original`/`download_error` без повторов.
4. Запуск `internal/extractor`: `pdfinfo/pdftotext/pdftoppm`, таймауты и путь из `extractor.*`, режим `mode` (форсируется `bbox`, если `detect_voting_tables=true`), диапазон `pages`, `max_parallel_commands`, `temp_dir`; при ошибке — `failed` без ретрая.
5. Выгрузка артефактов: `tables.raw.json`, `tables.normalized.json`, `tables.md` (с учётом переопределённого `markdown_bucket/object_key`), `manifest.json`, `debug/*` в `artifacts_bucket/artifacts_prefix`. Ошибка загрузки — `failed_retryable` с последующим ретраем.
6. Успех — статус `done` и `finished_at`. Maintenance-цикл (запускается на старте и по таймеру) восстанавливает зависшие `processing` старше `service.processing_timeout` (или сразу при старте): при превышении `retry.max_attempts` помечает `failed`, иначе возвращает в `pending` с `next_run_at = now + backoff(attempts, retry.backoff_interval, retry.max_backoff)` и `status_reason=restored_after_timeout`. Ретраи для `failed/failed_retryable` при `attempts < retry.max_attempts` переводят задачу в `pending` с `next_run_at = now + retry.backoff_interval`. Очистка удаляет `done/failed` старше `cleanup_retention`.

## Ключевые параметры конфигурации
- `service.queue_limit` — ограничение на активные задачи (`pending/processing`).
- `service.max_workers` — параллельные обработчики очереди.
- `service.processing_timeout` — таймаут одной обработки (используется в maintenance).
- `minio.artifacts_bucket` — бакет для артефактов по умолчанию.
- `minio.artifacts_prefix_template` — шаблон пути артефактов (переменные: `DocumentID`, `QueueID`, `DefaultPrefix`).
- `extractor.mode` / `extractor.pages` / `extractor.detect_voting_tables` — режимы извлечения и выбор страниц (`detect_voting_tables` включает парсер голосований и принудительно ставит `bbox`).
- Дополнительно: `extractor.pdfinfo_timeout/pdftotext_timeout/pdftoppm_timeout`, `extractor.max_parallel_commands`, `service.retry.*` (лимиты повторов и бэк-офф), `service.processing_timeout` (таймаут тика воркера и порог для восстановления) и `cleanup_retention`.
