# Table Extractor Service — Use cases

`table-extractor-service` извлекает таблицы из PDF (в первую очередь “таблицы голосования/протоколы”) с помощью poppler (pdfinfo/pdftotext или твой текущий пайплайн), нормализует структуру и сохраняет артефакты в MinIO.

Сервис асинхронный: gRPC-метод ставит задачу в очередь (PostgreSQL), воркеры обрабатывают задачи, клиент опрашивает статус. Поле `wait_for_result` принимается, но метод сейчас всегда возвращает сразу после постановки в очередь.

---

## Термины

- **original** — исходный PDF (MinIO bucket/object_key).
- **artifacts** — результаты извлечения таблиц (JSON/Markdown/manifest/debug).
- **job** — запись в очереди `table_extraction_queue`.
- **correlation_id** — берётся из gRPC metadata `x-correlation-id` или генерируется; возвращается в ответе.
- **config.yaml** — единый источник настроек: порты gRPC, лимиты очереди, таймауты воркера, MinIO/DB, observability.
- **metrics/tracing** — опциональные OTEL метрики/трейсы; логирование включается флагом `observability.logging_enabled`.

---

## Статусы очереди

- `pending` — ждёт обработки (может иметь `next_run_at` для отложенного старта).
- `processing` — воркер взял job и выполняет извлечение.
- `done` — артефакты записаны в MinIO.
- `failed` — необратимая ошибка (битый PDF, пароль, неподдерживаемый формат, превышены лимиты ретраев).
- `failed_retryable` (опционально) — временная ошибка; будет повтор (по `max_failed_retries`).

> Можно упростить до `pending/processing/done/failed`, как у тебя сейчас, но на практике `failed_retryable` помогает не смешивать “битый PDF” и “MinIO временно упал”.

### Очередь и ограничения

- Активные задачи (`pending/processing`) ограничены `service.queue_limit`; при превышении возвращается `ResourceExhausted`.
- Идемпотентность обеспечивается уникальным индексом `uq_table_extraction_queue_active_document` по `document_id`.
- `service.batch_size` управляет количеством задач за итерацию воркера; `service.max_workers` — параллелизм.
- `service.processing_timeout` + `retry.*` задают правила восстановления зависших задач и бэк‑оффов при ретраях.

---

## Use case 1 — Постановка задачи на извлечение таблиц из PDF

### Акторы
- **Gateway-service / document-service / stage-worker** как gRPC клиент.

### gRPC
`ExtractTablesFromPdf` (async enqueue + optionally wait)

### Валидация
- `document_id` обязателен.
- Источник PDF должен быть указан как пара `original_bucket` + `original_object_key` **или** как `source_url` (HTTP(S)/file). Для внутренних сценариев есть `local_path`, но его нет в публичном gRPC-контракте.
- Расширение вычисляется из пути (`original_object_key`/`source_url`/`local_path`) и должно быть `pdf`, иначе `InvalidArgument`.
- `artifacts_bucket` берётся из запроса или из конфигурации; если в запросе и конфиге пусто — `InvalidArgument`.
- `markdown_bucket/markdown_object_key` задаются только вместе; если оба пустые — `tables.md` пишется в тот же bucket/prefix, что и остальные артефакты.
- При превышении лимита очереди (`pending`/`processing` >= `service.queue_limit`, если задан) — `ResourceExhausted`.

### Поведение / идемпотентность
- Если для `document_id` уже есть активная задача в статусе `pending` или `processing`, она возвращается как результат вызова без создания новой записи (параметры запроса не переопределяются).
- Новая запись создаётся, если предыдущая задача завершена (`done/failed/failed_retryable`).
- Итоговые `artifacts_bucket/prefix` берутся из найденной активной задачи либо рассчитываются из запроса/конфига с учётом `minio.artifacts_prefix_template` или дефолтного `minio.default_artifacts_prefix/<document_id>/<queue_id>`.

### Шаги
1. Принять gRPC `ExtractTablesFromPdf(document_id, original_bucket/object_key?, source_url?, artifacts_bucket?, artifacts_prefix?, markdown_bucket/object_key?, wait_for_result?)`.  
   `x-correlation-id` читается из metadata; если пуст — генерируется и возвращается в ответе.
2. Проверить, что источник указан (bucket+key или `source_url`) и что расширение пути — `pdf`.
3. Рассчитать `artifacts_prefix`: если не передан — применить `minio.artifacts_prefix_template` или `minio.default_artifacts_prefix/<document_id>/<queue_id>`.
4. Создать запись в `table_extraction_queue` со статусом `pending`, путями источника и артефактов (включая переопределённый `markdown_bucket/object_key`, если задан), `correlation_id`.
5. Вернуть `queue_id`, `status=pending`, `correlation_id`, фактические `artifacts_bucket/prefix` и рассчитанные пути артефактов. Поле `wait_for_result` пока игнорируется: метод всегда отвечает сразу после постановки задачи.

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
    - `status`, `status_reason`, `created_at`, `updated_at`
    - `artifacts` с путями `tables.raw.json`, `tables.normalized.json`, `tables.md`, `manifest.json`, `debug/*`
    - `correlation_id` (пробрасывается в gRPC-headers и тело ответа).

### Сценарий 2.1 — Polling статуса

1. Клиент вызывает `GetTableExtractionJob(queue_id)` с backoff.
2. При `pending/processing` повторяет запрос через задержку (учитывая клиентский таймаут).
3. При `done/failed` скачивает артефакты из MinIO и отображает результат или ошибку.
4. При `NotFound` проверяет, не очищена ли запись по `cleanup_retention` или неверен `queue_id`.

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
1. Забрать job из очереди (в Postgres — `SELECT ... FOR UPDATE SKIP LOCKED`) с фильтром `status=pending` и `next_run_at <= now()`.
2. Перевести job в `processing`, увеличить `attempts`, обнулить `next_run_at`.
3. Определить источник PDF:
    - по умолчанию `original_bucket/original_object_key`;
    - если задан `source_url` → загрузить HTTP(S)/file (без аутентификации, короткий таймаут);
    - если в записи указан `local_path` (внутренний сценарий) → использовать его.
4. Скачать PDF во временный файл (MinIO/HTTP(S)/файловая система) через `filecache`.
5. Быстрые проверки:
    - `pdfinfo` (валидность/кол-во страниц/защищён ли паролем).
6. Извлечь таблицы:
    - poppler: `pdftotext` в режиме `-layout` или `-bbox` (если включён `extractor.detect_voting_tables`), при необходимости `pdftoppm` для рендера галочек;
    - парсер нормализует таблицы и строит Markdown.
7. Сохранить артефакты в MinIO в `artifacts_bucket/artifacts_prefix` (Markdown может складываться в отдельный bucket/object при переопределении):
    - `tables.raw.json`
    - `tables.normalized.json`
    - `tables.md`
    - `manifest.json`
    - (опционально) `debug/*`.
8. Обновить job: `status=done`, `finished_at`, `updated_at`.
9. При ошибках:
    - проблема источника/формата (битый PDF, неверное расширение, ошибка скачивания) → `failed` без повторов;
    - ошибки загрузки артефактов в MinIO → `failed_retryable` (maintenance вернёт в `pending` с бэк-оффом);
    - в статусе фиксируются `status_reason/error_code/error_message`.

### Сценарий 3.1 — Метрики и наблюдаемость воркера

- Логировать `queue_id`, `document_id`, `correlation_id`, длительность загрузки/извлечения/записи.
- Трассировка gRPC включается через `observability.tracing.enabled`; дополнительная телеметрия может быть добавлена при необходимости.

---

## Use case 4 — Maintenance loop (восстановление, ретраи, очистка)

### Акторы
- воркер `internal/maintenance`.

### Шаги (периодически)
1. `RestoreStuckProcessing(processing_timeout, max_attempts, backoff)`:
    - ищет задачи `processing`, обновлённые раньше `now-processing_timeout`;
    - если `attempts >= max_attempts` → помечает `failed` с `finished_at`;
    - иначе переводит в `pending` с `next_run_at=now()+backoff`.
2. `RetryFailed(max_attempts, retry_delay)`:
    - берёт `failed` и `failed_retryable` с `attempts < max_attempts`;
    - переводит их в `pending`, выставляя `next_run_at=now()+retry_delay`.
3. `CleanupFinished(retention)`:
    - удаляет записи в статусах `done/failed` старше `cleanup_retention` (артефакты в MinIO остаются).

### Сценарий 4.1 — Проверка лимитов и идемпотентности

1. Установить `service.queue_limit=1`.
2. Отправить первую задачу — ожидается `pending`.
3. Отправить вторую задачу с тем же `document_id` — ожидается возврат существующей записи без дубля.
4. Отправить третью задачу с другим `document_id`, пока первая в `pending/processing` — ожидается `ResourceExhausted`.

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

## Запуск и ручное тестирование

1. Скопировать `config/config.yaml.example` → `config/config.yaml`, указать MinIO (`artifacts_bucket`, `default_artifacts_prefix`), базу (`sqlite` или `postgres`), таймауты `processing_timeout`/`retry`.
2. Запустить сервис и воркер: `CONFIG_PATH=config/config.yaml go run ./cmd/main.go` (gRPC поднимется на `service.port`).
3. Отправить задачу через `grpcurl` (пример в README/test.md) и зафиксировать `queue_id` и `correlation_id`.
4. Мониторить статус через `GetTableExtractionJob(queue_id)` до `done/failed`, скачать `tables.md`/`manifest.json` из MinIO и сверить с эталоном `fixtures/`.
5. Негативные проверки: передать `.txt` вместо PDF → `InvalidArgument`; превысить `queue_limit` → `ResourceExhausted`; симулировать недоступность MinIO → задача уходит в `failed_retryable` и восстанавливается maintenance-циклом.
