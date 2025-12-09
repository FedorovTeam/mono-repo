# Flows

## Конвертация DOCX → Markdown через очередь
1. Gateway/Temporal вызывает gRPC метод `ConvertDocumentToMarkdown` с `document_id`, `original_bucket`, `original_object_key`, `filename_extension` и опционально `markdown_bucket`/`markdown_object_key`.
2. gRPC слой валидирует обязательные поля, нормализует расширение файла и проверяет лимит слотов (`service.max_workers`). При переполнении возвращается `ResourceExhausted`.
3. В PostgreSQL добавляется запись в `transformations_queue` со статусом `pending` (поля: `document_id`, `original_bucket/key`, `markdown_bucket/key`, `filename_extension`, `docling_job_id`, `docling_poll_count`, `next_poll_at`). В ответе клиент получает `queue_id`, статус `pending` и путь для Markdown; поля Docling заполняются после первого обращения к внешнему сервису и используются для безопасного продолжения опроса при восстановлении задач.
4. Фоновый воркер с периодом опроса читает по одной задаче `SELECT ... FOR UPDATE SKIP LOCKED`, обновляет статус на `processing` и фиксирует `started_at`. В выборку попадают и задачи в статусе `waiting_docling` с наступившим `next_poll_at`.
5. Воркер скачивает исходник из MinIO с коротким таймаутом, отправляет документ в Docling (`POST /v1/convert/file` с заголовками `X-Correlation-Id`, `X-Docling-Source-Filename`, `X-Api-Key`) и сохраняет ответ в MinIO. Если Docling ещё обрабатывает документ, задача переводится в статус `waiting_docling` с новым `next_poll_at`, чтобы воркеры не блокировались на длительном ожидании.
6. После успешной загрузки статус в очереди меняется на `done` с заполнением `finished_at`. При ошибке конвертации/загрузки — статус `failed` и текст ошибки в поле `error`.
7. Отдельный фоновый цикл каждые `service.cleanup_interval` возвращает в работу задачи `waiting_docling` с просроченным `next_poll_at`, переводит просроченные Docling-задачи в `failed` и восстанавливает зависшие `processing` (использует `service.processing_timeout`).

## SLA и таймауты
- `service.processing_timeout` — максимальное время нахождения задачи в статусе `processing`; по превышению задача восстанавливается в `pending` (или переводится в `failed`, если превышен лимит ретраев или опросов Docling).
- `service.io_timeout` — ограничение на быстрые операции (скачивание/загрузка, submit в Docling), не влияющее на ожидание внешнего сервиса.
- `service.cleanup_interval` — период фонового цикла, который выполняет восстановление зависших задач и очистку завершённых.
- `docling.status_initial_interval` / `docling.status_backoff_factor` / `docling.status_max_interval` — параметры экспоненциального backoff для опроса статуса Docling (рост до 30–60 минут при ожидании 1–2 часов).
- `docling.status_max_attempts` — ограничение на общее число запросов статуса (0 — без ограничений).
- `docling.max_job_lifetime` — максимальное время нахождения задачи Docling в ожидании; по превышению задача помечается как `failed`.

## Управление конфигурацией
1. При старте `cmd/server` читает `config.yaml` (или путь из `CONFIG_PATH`).
2. Подключения к Docling, MinIO и PostgreSQL берут параметры из конфига, без `.env`.
3. Любые изменения конфига требуют перезапуска сервера; формат задокументирован в README.
