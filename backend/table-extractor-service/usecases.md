# Table Extractor Service — Use cases

`table-extractor-service` извлекает таблицы из PDF (в первую очередь “таблицы голосования/протоколы”) с помощью poppler (pdfinfo/pdftotext или твой текущий пайплайн), нормализует структуру и сохраняет артефакты в MinIO.

Сервис асинхронный: gRPC-метод ставит задачу в очередь (PostgreSQL), воркеры обрабатывают задачи, клиент опрашивает статус. Опционально поддерживается `wait_for_result=true` (polling внутри gRPC метода).

---

## Термины

- **original** — исходный PDF (MinIO bucket/object_key).
- **artifacts** — результаты извлечения таблиц (JSON/Markdown/manifest/debug).
- **job** — запись в очереди `table_extraction_queue`.
- **correlation_id** — берётся из gRPC metadata `x-correlation-id` или генерируется; возвращается в ответе.

---

## Статусы очереди

- `pending` — ждёт обработки (может иметь `next_poll_at` для отложенного старта).
- `processing` — воркер взял job и выполняет извлечение.
- `done` — артефакты записаны в MinIO.
- `failed` — необратимая ошибка (битый PDF, пароль, неподдерживаемый формат, превышены лимиты ретраев).
- `failed_retryable` (опционально) — временная ошибка; будет повтор (по `max_failed_retries`).

> Можно упростить до `pending/processing/done/failed`, как у тебя сейчас, но на практике `failed_retryable` помогает не смешивать “битый PDF” и “MinIO временно упал”.

---

## Use case 1 — Постановка задачи на извлечение таблиц из PDF

### Акторы
- **Gateway-service / document-service / stage-worker** как gRPC клиент.

### gRPC
`ExtractTablesFromPdf` (async enqueue + optionally wait)

### Валидация
- `document_id` обязателен.
- `original_bucket` и `original_object_key` обязательны.
- Расширение должно быть `pdf` (либо `filename_extension=pdf`, либо вычисляется из `original_object_key`). Иначе `InvalidArgument`.
- `artifacts_bucket` берётся из запроса или из конфигурации; если оба пустые — `InvalidArgument`.
- `source_bucket/source_object_key` (если присутствуют) заполняются только вместе.
- `source_url` взаимоисключён с `source_bucket/source_object_key`.
- При превышении лимита очереди (`pending`/`processing` >= `service.queue_limit`, если задан) — `ResourceExhausted`.

### Поведение / идемпотентность
- Если для `document_id` уже есть **последняя** задача не в статусе `failed` (или `failed_retryable`, если он включён), она возвращается как результат вызова без создания новой записи.
- Выходные `artifacts_bucket/prefix` берутся из найденной задачи или из запроса/конфига.

### Шаги
1. Принять gRPC `ExtractTablesFromPdf(document_id, original_bucket, original_object_key, artifacts_bucket?, artifacts_prefix?, options?, wait_for_result?)`.  
   `x-correlation-id` читается из metadata или генерируется.
2. Нормализовать `filename_extension` (если передано) или вычислить из ключа; проверить `pdf`.
3. Вычислить `artifacts_prefix`, если не передан:
    - рекомендуемый шаблон: `tables/<document_id>/<queue_id>/`
4. Создать запись в `table_extraction_queue` со статусом `pending`, путями original и artifacts, опциями извлечения, `correlation_id`.
5. Вернуть `queue_id`, `status`, `correlation_id`, фактические `artifacts_bucket` и `artifacts_prefix`.
6. Если `wait_for_result=true`, сервис опрашивает `GetJob(queue_id)` с backoff (учитывая `next_poll_at`) до `done/failed`.  
   При тайм-ауте/исчерпании попыток — `DeadlineExceeded` с текстом `table extraction result not ready`.

---

## Use case 2 — Получение статуса задачи и ссылок на артефакты

### Акторы
- gRPC клиент (Gateway/document-service/UI).

### gRPC
`GetTableExtractionJob(queue_id)`.

### Шаги
1. Принять `GetTableExtractionJob(queue_id)`.
2. Прочитать запись из БД.
3. Вернуть:
    - `status`, `attempt`, `locked_at/locked_by` (если нужно), `last_error`
    - `artifacts_bucket`, `artifacts_prefix`
    - список артефактов (если `done`): `tables.md`, `tables.normalized.json`, `manifest.json`, `debug/*` (опционально).

---

## Use case 3 — Фоновая обработка очереди (worker loop)

### Акторы
- воркер `internal/worker`.

### Подготовка при старте
- Все задачи в `processing` переводятся обратно в `pending` (чтобы не зависали после рестарта).
- Сервис запускает:
    - `workerLoop` (N воркеров)
    - `maintenanceLoop` (периодические процедуры восстановления/очистки)

### Шаги workerLoop
1. Забрать job из очереди:
    - `SELECT ... FOR UPDATE SKIP LOCKED`
    - берём `pending` где `next_poll_at <= now()` (если поле используется)
2. Перевести job в `processing`, увеличить `attempt`, записать `locked_at/locked_by`.
3. Определить источник PDF:
    - по умолчанию `original_bucket/original_object_key`
    - если задан `source_bucket/source_object_key` → использовать его
    - если задан `source_url` → скачать по HTTP (без кредов; быстрый тайм-аут)
4. Скачать PDF во временный файл (если источник MinIO/URL).
5. Быстрые проверки:
    - `pdfinfo` (валидность/кол-во страниц/защищён ли паролем)
6. Извлечь таблицы:
    - poppler (например `pdftotext -layout` + твой парсер табличных блоков)
    - поддержать режимы из options: `pages`, `layout_mode`, `y_tolerance`, `merge_cells`, `detect_voting_table`, etc.
7. Нормализовать структуру:
    - выравнивание колонок, склейка строк, trim/cleanup пробелов
    - детект “таблица голосования” (если включён) и отдельный рендер
8. Сохранить артефакты в MinIO в `artifacts_bucket/artifacts_prefix`:
    - `tables.raw.json`
    - `tables.normalized.json`
    - `tables.md`
    - `manifest.json` (версия алгоритма, poppler version, страницы, метрики качества)
    - (опционально) `debug/page-*.txt`
9. Обновить job: `status=done`, `finished_at`, `result_meta` (кол-во таблиц/страниц).
10. При ошибке:
- если ошибка формата/пароль/битый PDF → `failed` (final)
- если временная (MinIO/network/IO) → `failed_retryable` или вернуть в `pending` с `next_poll_at=now()+backoff`
- записать `last_error`, `error_code`.

---

## Use case 4 — Maintenance loop (восстановление, ретраи, очистка)

### Акторы
- воркер `internal/maintenance`.

### Шаги (периодически)
1. Метрики очереди: `CountByStatus`.
2. `RestoreStuckProcessing(processing_timeout)`:
    - `processing` старше таймаута:
        - если `attempt >= scheduler.max_processing_retries` → `failed`
        - иначе → `pending` (+ `next_poll_at` backoff)
3. `RetryFailed(olderThan=now-failed_retry_delay, max_failed_retries)`:
    - `failed_retryable` или “failed с retry-флагом” возвращать в `pending`.
4. `CleanupFinished(retention)`:
    - удалять/архивировать `done/failed` старше `cleanup_retention` (по желанию).
    - артефакты в MinIO можно не удалять автоматически (или удалять отдельным джобом).

---

## Use case 5 — Интеграция с Document pipeline (опционально)

### Акторы
- stage-worker (convert_to_markdown) или post-processor.

### Сценарий
1. После конвертации Docling (markdown готов) поставить table-extractor job для PDF (если документ — протокол/таблицы).
2. Дождаться `done` (polling).
3. Выполнить merge (если нужен) отдельным модулем/сервисом:
    - взять `docling markdown`
    - вставить/заменить таблицы на `tables.md`
4. Сохранить итоговый markdown в MinIO и продолжить публикацию в GitHub.

> Важно: table-extractor-service сам **не обязан** мёрджить с docling markdown — это отдельная ответственность, чтобы сервис оставался простым и детерминированным.

---
