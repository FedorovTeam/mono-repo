# Table Extractor Service — Flows

Документ описывает основные потоки сервиса и ключевые параметры конфигурации для постановки задач извлечения таблиц и их обработки воркером.

## Поток: ExtractTablesFromPdf → очередь
1. gRPC вызов `ExtractTablesFromPdf(document_id, original_bucket, original_object_key, artifacts_bucket?, artifacts_prefix?, markdown_bucket/object_key?, wait_for_result?)`.
2. Валидация: `document_id` и источник PDF обязательны; расширение должно быть `pdf`; `markdown_bucket` и `markdown_object_key` передаются только вместе.
3. Определение `artifacts_bucket` (из запроса или `minio.artifacts_bucket`), расчёт `artifacts_prefix` через `minio.artifacts_prefix_template` (`{{ .DefaultPrefix }}/{{ .DocumentID }}/{{ .QueueID }}` по умолчанию) или fallback `minio.default_artifacts_prefix/<document_id>/<queue_id>`.
4. Постановка в `table_extraction_queue` со статусом `pending`, idempotency по `document_id` (уникальный индекс на `pending/processing`). При переполнении — `ResourceExhausted` (`service.queue_limit`).
5. Ответ содержит `queue_id`, `status`, `artifacts_bucket/prefix`, `markdown_bucket/object_key` (если задан), `correlation_id`. `wait_for_result` остаётся опциональным polling в хэндлере.

## Поток: worker loop
1. `SelectPendingBatchForUpdate(... FOR UPDATE SKIP LOCKED)` с учётом `service.batch_size`/`service.max_workers`.
2. Перевод в `processing`, инкремент `attempts`, сброс `next_run_at`.
3. Загрузка PDF из MinIO через `internal/storage`, учёт `correlation_id`.
4. Запуск `internal/extractor`: `pdfinfo/pdftotext/pdftoppm`, режим `extractor.mode`, диапазон `extractor.pages`, флаг `extractor.detect_voting_tables`.
5. Выгрузка артефактов: `tables.raw.json`, `tables.normalized.json`, `tables.md` (в `markdown_bucket/object_key` или в `artifacts_prefix`), `manifest.json`, `debug/*`.
6. Обновление статуса: `done` или `failed/failed_retryable`, запись `error`/`status_reason`, `finished_at`. Maintenance цикл восстанавливает зависшие задачи (`service.processing_timeout`), делает retry с бэк-оффом и чистит записи по `cleanup_retention`.

## Ключевые параметры конфигурации
- `service.queue_limit` — ограничение на активные задачи (`pending/processing`).
- `service.max_workers` — параллельные обработчики очереди.
- `service.processing_timeout` — таймаут одной обработки (используется в maintenance).
- `minio.artifacts_bucket` — бакет для артефактов по умолчанию.
- `minio.artifacts_prefix_template` — шаблон пути артефактов (переменные: `DocumentID`, `QueueID`, `DefaultPrefix`).
- `extractor.mode` / `extractor.pages` / `extractor.detect_voting_tables` — режимы извлечения и выбор страниц.
- Дополнительно: `extractor.pdfinfo_timeout/pdftotext_timeout/pdftoppm_timeout`, `extractor.max_parallel_commands`, `service.retry.*` и `cleanup_retention`.
