# Flows

## Конвертация DOCX → Markdown через очередь
1. Gateway или cron-воркер `document-service` вызывает gRPC метод `ConvertDocumentToMarkdown` с `document_id`, `original_bucket`, `original_object_key`, `filename_extension` и опционально `markdown_bucket`/`markdown_object_key`.
2. gRPC слой валидирует обязательные поля, нормализует расширение файла (или определяет его из `original_object_key`), проверяет поддержку формата и лимит очереди (`service.queue_limit`) через `CountByStatus` для статусов `pending`/`processing`/`waiting_docling`. При переполнении возвращается `ResourceExhausted`. В отсутствие `markdown_bucket`/`markdown_object_key` подставляются значения по умолчанию из конфига и `BuildMarkdownKey`.
3. В PostgreSQL добавляется запись в `transformations_queue` со статусом `pending` (ключевые поля: `document_id`, `original_bucket/key`, `markdown_bucket/key`, `filename_extension`, `retry_count`, `correlation_id`, `docling_job_id`, `docling_poll_count`, `next_poll_at`). Клиент получает `queue_id`, статус `pending` и путь для Markdown. Correlation ID фиксируется для трассировки и восстановления после перезапуска.
4. При старте процесса воркеры снимают блокировки с задач в статусе `processing`, переводя их в `pending`: задачи без Docling-чекпоинта будут выполнены заново, а задачи с заполненным `docling_job_id` продолжат опрос состояния без повторной загрузки файла.
5. Фоновый воркер по таймеру выбирает по одной задаче `FOR UPDATE SKIP LOCKED`, ставит ей статус `processing`, увеличивает `retry_count` для новых попыток и сбрасывает `next_poll_at`. В выборку попадают и задачи `waiting_docling`, у которых наступил `next_poll_at`.
6. Если у задачи нет `docling_job_id`, воркер скачивает исходник из MinIO с коротким таймаутом, собирает имя файла и отправляет документ в Docling на `/v1/convert/file/async` с заголовками `X-Correlation-Id`, `X-Docling-Source-Filename`, `X-Docling-Endpoint` и `X-Api-Key` (при наличии). После успешного `submit` сохраняется `docling_job_id`, `docling_poll_count=0`, рассчитывается `next_poll_at` и задача переводится в `waiting_docling` без повторной загрузки файла.
7. Если `docling_job_id` уже есть, воркер опрашивает `/v1/status/poll/{job_id}`. При промежуточных статусах выставляется новый `next_poll_at` по экспоненциальному backoff и задача остаётся в `waiting_docling`. При успехе воркер запрашивает `/v1/result/{job_id}`, определяет медиатип (по умолчанию `text/markdown; charset=utf-8`), загружает результат в MinIO и ставит статус `done`. При ошибках фиксируется `failed` с описанием причины; превышение лимитов по опросам или таймаутам также попадает в ошибку.
8. Отдельный фоновый цикл каждые `service.cleanup_interval` снимает метрики очереди, возвращает в работу задачи `waiting_docling` с наступившим `next_poll_at`, помечает просроченные по `docling.max_job_lifetime` как `failed`, восстанавливает либо завершает зависшие `processing` (использует `service.processing_timeout`, `service.max_processing_retries`, `docling.status_max_attempts`) и удаляет завершённые (`done`/`failed`) старше `service.retention_period`.

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
