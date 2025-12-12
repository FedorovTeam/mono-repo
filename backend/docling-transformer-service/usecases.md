# Use cases

## Постановка задачи на конвертацию
- **Акторы:** Gateway-service (gRPC клиент).
- **Валидация:**
  - `document_id` обязателен.
  - `original_bucket` и `original_object_key` обязательны.
  - `filename_extension` берётся из запроса или вычисляется из `original_object_key`; поддерживаются только `doc` / `docx` / `pdf` (иначе `InvalidArgument`).
  - `markdown_bucket` берётся из запроса или из конфигурации; если оба пустые — `InvalidArgument`.
  - При превышении лимита очереди (`pending`/`processing`/`waiting_docling` >= `service.queue_limit`, если он задан) возвращается `ResourceExhausted`.
- **Шаги:**
  1. Принять gRPC вызов `ConvertDocumentToMarkdown` с обязательными `document_id`, `original_bucket`, `original_object_key`, опциональными `markdown_bucket`, `markdown_object_key`, `filename_extension`; `x-correlation-id` читается из метаданных или генерируется заново.
  2. Нормализовать расширение и вычислить `markdown_object_key`, если не передан (замена исходного расширения на `.md`).
  3. Создать запись в таблице `transformations_queue` со статусом `pending`, путём исходника и результата, нормализованным расширением и прокинутым `correlation_id`.
  4. Вернуть `queue_id`, `status=pending`, а также фактические `markdown_bucket` и `markdown_object_key` в ответе; само преобразование выполняется фоном.

## Фоновая обработка очереди
- **Акторы:** Воркер сервиса (`internal/worker`).
- **Подготовка:** при запуске выполняет обслуживающий цикл: возвращает в работу задачи `waiting_docling` с наступившим `next_poll_at`, помечает просроченные по `docling.max_job_lifetime` как `failed` и обрабатывает зависшие `processing`, только если они превысили `service.processing_timeout` с учётом `scheduler.max_processing_retries` и `docling.status_max_attempts`.
- **Шаги:**
  1. Забрать задачу из очереди (`pending` либо `waiting_docling` c наступившим `next_poll_at`) с блокировкой `FOR UPDATE SKIP LOCKED` и пометкой `processing`.
  2. Проставить отсутствующие значения: `markdown_bucket` из конфигурации, `markdown_object_key` из исходного пути, расширение и `Content-Type` через `ResolveContentTypeByExtension`.
  3. Скачивать оригинал из MinIO (`storage.Download`) с быстрым таймаутом; имя файла берётся из `original_object_key` или формируется как `document.<ext>`.
  4. Отправить документ в Docling через POST `<docling.endpoint>/v1/convert/file/async` с multipart-полем `files`, заголовками `X-Correlation-Id`, `X-Docling-Source-Filename`, `X-Docling-Endpoint`, `X-Api-Key` (если задан), опциями Markdown/таблиц и OCR для PDF (lang/ocr_engine/pdf_backend).
  5. Сохранить `docling_job_id`, поллинг-счётчик и `next_poll_at` в очереди; вернуть задачу в статус `waiting_docling`.

## Опрос Docling и завершение
- **Акторы:** Воркер сервиса.
- **Шаги:**
  1. Делать запрос статуса `GET <docling.endpoint>/v1/status/poll/{job_id}` с теми же заголовками. Ошибки 5xx/таймауты фиксируются как временные: задача откладывается с экспоненциальным `next_poll_at`, поллинг-счётчик обновляется.
  2. При статусах `pending`/`processing` задача возвращается в `waiting_docling` до наступления `next_poll_at` (ограничение по количеству попыток `docling_max_attempts`).
  3. При успехе получать результат `GET <docling.endpoint>/v1/result/{job_id}` и извлекать контент в порядке приоритета: `md_content` → `html_content` → `text_content`, дополняя `charset`.
  4. Нормализовать `media_type` (по умолчанию `text/markdown; charset=utf-8`) и загрузить результат в MinIO (`storage.Upload`) в `markdown_bucket/object_key` из задачи.
  5. Обновить задачу статусом `done` в очереди; при любой ошибке — `failed` с текстом причины. Метрики фиксируют длительность обработки, ожидание Docling и счётчики временных ретраев.

## Возобновление после рестартов
- **Акторы:** Воркер сервиса.
- **Шаги:**
  1. При старте и далее по расписанию обрабатывать зависшие `processing` задачи по `service.processing_timeout`: если превышены лимиты `scheduler.max_processing_retries` или `docling.status_max_attempts`, они получают статус `failed`, иначе возвращаются в `pending`.
  2. Периодически активировать просроченные `waiting_docling` задачи (по `next_poll_at` или превышению `docling.max_job_lifetime`).
  3. Удалять завершённые задачи (`done`/`failed`) старше периода хранения (`cleanup_retention`).
