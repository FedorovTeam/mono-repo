# Use cases

## Постановка задачи на конвертацию
- **Акторы:** Gateway-service (gRPC клиент).
- **Валидация:**
  - `document_id` обязателен.
  - `original_bucket` и `original_object_key` обязательны.
  - `filename_extension` берётся из запроса или вычисляется из `original_object_key`; поддерживаются только `doc` / `docx` / `pdf` (иначе `InvalidArgument`).
  - `markdown_bucket` берётся из запроса или из конфигурации; если оба пустые — `InvalidArgument`.
  - Поля источника: `source_bucket` и `source_object_key` заполняются только вместе, `source_url` взаимоисключён с ними.
  - При превышении лимита очереди (`pending`/`processing`/`waiting_docling` >= `service.queue_limit`, если он задан) возвращается `ResourceExhausted`.
- **Ответ:** всегда включает `correlation_id`; при наличии результата возвращает `table_metadata` (а также плоские `table_count` и `table_pages`) из сохранённой задачи.
- **Поведение:** если уже есть последняя задача для `document_id` не в статусе `failed`, она возвращается как результат вызова без создания новой записи; выходные bucket/object_key берутся из задачи или из запроса/конфига.
- **Модель:** задача ставится в таблицу `transformations_queue`, а реальная конвертация выполняется воркером через Docling по цепочке *submit → status poll → result* без дополнительных внутренних этапов.
- **Шаги:**
1. Принять gRPC вызов `ConvertDocumentToMarkdown` с обязательными `document_id`, `original_bucket`, `original_object_key`, опциональными `markdown_bucket`, `markdown_object_key`, `filename_extension`, флагом `wait_for_result`, а также источником (`source_bucket`/`source_object_key` либо `source_url`) без передачи кредов; `x-correlation-id` читается из метаданных или генерируется заново и возвращается в ответе.
  2. Нормализовать расширение и вычислить `markdown_object_key`, если не передан (замена исходного расширения на `.md`).
  3. Создать запись в таблице `transformations_queue` со статусом `pending`, путём исходника и результата, нормализованным расширением и прокинутым `correlation_id`.
  4. Вернуть `queue_id`, `status`, `correlation_id`, а также фактические `markdown_bucket` и `markdown_object_key` (берутся из сохранённой задачи, если она уже создана или завершается). При наличии метаданных таблиц вернуть их в полях `table_metadata`/`table_count`/`table_pages`. При `wait_for_result=true` use case опрашивает очередь по `status_backoff` с учётом `next_poll_at`, дожидаясь `done` или `failed`; при исчерпании попыток или тайм-ауте — `DeadlineExceeded` с сообщением `conversion result not ready`.

## Синхронная конвертация DOCX
- **Акторы:** Gateway-service (gRPC клиент).
- **Валидация:**
  - Ровно один из `docx_content` или `docx_url` должен быть заполнен.
  - Если передан `docx_url`, он скачивается HTTP‑клиентом; любые не‑2xx ответы или пустое тело — `InvalidArgument`.
  - `filename` опционален; при отсутствии берётся из URL либо `document.docx`.
- **Шаги:**
  1. Принять gRPC вызов `ConvertDocxToMarkdown` с контентом файла или ссылкой и опциональным именем.
  2. Определить `Content-Type` по имени файла, отправить документ в Docling (`Submit`) и опрашивать статус (`CheckStatus`) с backoff `docling.status_*` до готовности.
  3. Вернуть готовый Markdown (`markdown`), `media_type`, `charset` и `correlation_id`; ошибки Docling или тайм-ауты пробрасываются вызывающей стороне (при исчерпании попыток — `DeadlineExceeded`).

## Фоновая обработка очереди
- **Акторы:** Воркер сервиса (`internal/worker`).
- **Подготовка:** при старте сервиса `processing` задачи восстанавливаются в `pending`; далее сервис циклически считает очереди, помечает просроченные `waiting_docling` старше `docling.max_job_lifetime` как `failed`, переводит зависшие `processing` в `pending` или `failed` (по `service.processing_timeout`, `scheduler.max_processing_retries`, `docling.status_max_attempts`), возвращает старые `failed` в работу (с `scheduler.max_failed_retries`) и удаляет завершённые задачи по `cleanup_retention`. `pending` с отложенным `next_poll_at` (например, после блокировки на submit) подхватываются только по наступлению времени.
- **Шаги:**
 1. Забрать задачу из очереди: приоритет на `waiting_docling` с наступившим `next_poll_at`; если в очереди есть любые `waiting_docling` (даже с будущим `next_poll_at`), новые `pending` не выдаются, а сами `pending` берутся только после наступления их `next_poll_at`.
 2. Проставить отсутствующие значения: `markdown_bucket` из конфигурации, `markdown_object_key` из исходного пути, расширение и `Content-Type` через `ResolveContentTypeByExtension`.
 3. Определить режим отправки в Docling: в `source_async` (или при наличии `source_bucket`/`source_object_key` без `source_url`) используется `convert/source/async` без скачивания файла, с передачей MinIO-кредов из конфига; иначе оригинал скачивается из MinIO/URL с быстрым таймаутом и именем из `original_object_key`/`source_url` (fallback `document.<ext>`).
 4. Отправить документ в Docling: перед вызовом берётся advisory-лок на отправку; при занятости локи задача возвращается в `pending` с `next_poll_at = now + submit_lock_delay`. Для загрузки файла — POST `<docling.endpoint>/v1/convert/file/async` с multipart `files`, заголовками `X-Correlation-Id`, `X-Docling-Source-Filename`, `X-Docling-Endpoint`, `X-Api-Key` (если задан) и минимальными полями `to_formats`/`from_formats`; для `source_async` — POST `<docling.endpoint>/v1/convert/source/async` с JSON, содержащим MinIO-источник/приёмник и те же опции форматов. Временные HTTP-ошибки Docling ретраятся, а при исчерпании попыток (`TemporarySubmitRetryExhausted`) задача переводится в `waiting_docling` без docling-id с `next_poll_at` по docling-backoff для повторного сабмита.
 5. После успешной отправки сохраняются `docling_task_id`/`docling_job_id`, счётчик поллинга и `next_poll_at`, статус — `waiting_docling`.

## Опрос Docling и завершение
- **Акторы:** Воркер сервиса.
- **Шаги:**
  1. Делать запрос статуса `GET <docling.endpoint>/v1/status/poll/{task_id|job_id}` (приоритет — `docling_task_id`, иначе `docling_job_id`) тем же конвертером, который сабмитил задачу; прогресс поллинга (`docling_poll_count`, идентификаторы) сохраняется в базе.
  2. Ошибки статуса/сетевые тайм-ауты учитываются: при двух одинаковых ошибках подряд задача переводится в `pending` с ростом `retry_count`, очисткой docling-id и задержкой `docling_requeue_delay`; прочие ожидания переносят `next_poll_at` по backoff и оставляют статус `waiting_docling`. Достижение лимита `docling.status_max_attempts` (даже без ошибок) также сбрасывает docling-id/счётчики через `RequeueDoclingWaiting` и планирует повторную отправку. Превышение `scheduler.docling_max_restarts` (считается от `retry_count`) приводит к ошибке `DoclingRestartsExceeded` и завершению задачи в `failed` без дальнейших попыток.
  3. При успехе получать результат `GET <docling.endpoint>/v1/result/{task_id|job_id}` и извлекать контент в порядке приоритета: `md_content` → `html_content` → `text_content`, дополняя `charset`; в процессе фиксировать `docling_poll_count` и идентификаторы в БД.
  4. Нормализовать `media_type` (по умолчанию `text/markdown; charset=utf-8`), собрать `table_metadata` (tables/pages) и загрузить результат в MinIO (`storage.Upload`) в `markdown_bucket/object_key`.
  5. Обновить задачу статусом `done` в очереди вместе с `table_metadata`/`table_count`/`table_pages`; при любой ошибке — `failed` с текстом причины. Метрики фиксируют длительность обработки, ожидание Docling и счётчики ретраев/реактиваций Docling.

## Возобновление после рестартов
- **Акторы:** Воркер сервиса.
- **Шаги:**
  1. При старте все задачи в `processing` переводятся в `pending`.
  2. По расписанию обрабатывать зависшие `processing` по `processing_timeout`: превышающие лимиты (`max_processing_retries` или `docling.status_max_attempts` по `docling_poll_count`) помечаются `failed`, остальные возвращаются в `pending`.
  3. `waiting_docling` старше `docling.max_job_lifetime` отмечаются как `failed` (они не переводятся в `pending`).
  4. Повторно ставить в очередь завершившиеся с ошибкой задачи старше `failed_retry_delay`, пока число попыток меньше `scheduler.max_failed_retries`; исчерпавшие лимит остаются в `failed` с расширенным описанием ошибки.
  5. Удалять завершённые задачи (`done`/`failed`) старше периода хранения (`cleanup_retention`).
