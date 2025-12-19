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
- **Поведение:** если уже есть последняя задача для `document_id` не в статусе `failed`, она возвращается как результат вызова без создания новой записи; выходные bucket/object_key берутся из задачи или из запроса/конфига.
- **Шаги:**
1. Принять gRPC вызов `ConvertDocumentToMarkdown` с обязательными `document_id`, `original_bucket`, `original_object_key`, опциональными `markdown_bucket`, `markdown_object_key`, `filename_extension`, флагом `wait_for_result`, а также источником (`source_bucket`/`source_object_key` либо `source_url`) без передачи кредов; `x-correlation-id` читается из метаданных или генерируется заново.
  2. Нормализовать расширение и вычислить `markdown_object_key`, если не передан (замена исходного расширения на `.md`).
  3. Создать запись в таблице `transformations_queue` со статусом `pending`, путём исходника и результата, нормализованным расширением и прокинутым `correlation_id`.
  4. Вернуть `queue_id`, `status` текущей задачи, а также фактические `markdown_bucket` и `markdown_object_key`. При `wait_for_result=true` use case дожидается `done`/`failed` по backoff-политике статусов очереди, либо отвечает `DeadlineExceeded` (текст `conversion result not ready`) при исчерпании попыток или тайм-ауте контекста.

## Синхронная конвертация DOCX
- **Акторы:** Gateway-service (gRPC клиент).
- **Валидация:**
  - Ровно один из `docx_content` или `docx_url` должен быть заполнен.
  - Если передан `docx_url`, он скачивается HTTP‑клиентом; любые не‑2xx ответы или пустое тело — `InvalidArgument`.
  - `filename` опционален; при отсутствии берётся из URL либо `document.docx`.
- **Шаги:**
  1. Принять gRPC вызов `ConvertDocxToMarkdown` с контентом файла или ссылкой и опциональным именем.
  2. Определить `Content-Type` по имени файла, отправить документ в Docling (`Submit`) и опрашивать статус (`CheckStatus`) с backoff `docling.status_*` до готовности.
  3. Вернуть готовый Markdown (`markdown`), `media_type` и `charset`; ошибки Docling или тайм-ауты пробрасываются вызывающей стороне.

## Фоновая обработка очереди
- **Акторы:** Воркер сервиса (`internal/worker`).
- **Подготовка:** при старте сервиса `processing` задачи восстанавливаются в `pending`; далее сервис циклически считает очереди, помечает просроченные `waiting_docling` старше `docling.max_job_lifetime` как `failed`, переводит зависшие `processing` в `pending` или `failed` (по `service.processing_timeout`, `scheduler.max_processing_retries`, `docling.status_max_attempts`), возвращает старые `failed` в работу (с `scheduler.max_failed_retries`) и удаляет завершённые задачи по `cleanup_retention`.
- **Шаги:**
 1. Забрать задачу из очереди: приоритет на `waiting_docling` с наступившим `next_poll_at`; если в очереди есть любые `waiting_docling` (даже с будущим `next_poll_at`), новые `pending` не выдаются.
 2. Проставить отсутствующие значения: `markdown_bucket` из конфигурации, `markdown_object_key` из исходного пути, расширение и `Content-Type` через `ResolveContentTypeByExtension`.
 3. Определить режим отправки в Docling: в `source_async` (или при наличии `source_bucket`/`source_object_key`) используется `convert/source/async` без скачивания файла, с передачей MinIO-кредов из конфига; иначе оригинал скачивается из MinIO/URL с быстрым таймаутом и именем из `original_object_key`/`source_url` (fallback `document.<ext>`).
  4. Отправить документ в Docling: для загрузки файла — POST `<docling.endpoint>/v1/convert/file/async` с multipart `files`, заголовками `X-Correlation-Id`, `X-Docling-Source-Filename`, `X-Docling-Endpoint`, `X-Api-Key` (если задан) и настройками Markdown/таблиц/OCR; для `source_async` — POST `<docling.endpoint>/v1/convert/source/async` с JSON, содержащим MinIO-источник/приёмник. Для PDF воркер принудительно включает OCR в запросе.
  5. После отправки сохраняются `docling_task_id`/`docling_job_id`, счётчик поллинга и `next_poll_at`, статус — `waiting_docling`.

## Опрос Docling и завершение
- **Акторы:** Воркер сервиса.
- **Шаги:**
  1. Делать запрос статуса `GET <docling.endpoint>/v1/status/poll/{task_id|job_id}` тем же конвертером, который сабмитил задачу; прогресс поллинга (`docling_poll_count`, идентификаторы) сохраняется в базе.
  2. Ошибки статуса/сетевые тайм-ауты учитываются: при повторяемых ошибках или превышении лимита попыток `docling.status_max_attempts` задача переводится в `pending` с ростом `retry_count` и очисткой docling-id для повторной отправки позже; иные ожидания переносят `next_poll_at` по backoff.
  3. При успехе получать результат `GET <docling.endpoint>/v1/result/{job_id}` и извлекать контент в порядке приоритета: `md_content` → `html_content` → `text_content`, дополняя `charset`.
  4. Нормализовать `media_type` (по умолчанию `text/markdown; charset=utf-8`) и загрузить результат в MinIO (`storage.Upload`) в `markdown_bucket/object_key`.
  5. Обновить задачу статусом `done` в очереди; при любой ошибке — `failed` с текстом причины. Метрики фиксируют длительность обработки, ожидание Docling и счётчики ретраев/реактиваций Docling.

## Возобновление после рестартов
- **Акторы:** Воркер сервиса.
- **Шаги:**
  1. При старте все задачи в `processing` переводятся в `pending`.
  2. По расписанию обрабатывать зависшие `processing` по `processing_timeout`: превышающие лимиты (`max_processing_retries` или `docling.status_max_attempts` по `docling_poll_count`) помечаются `failed`, остальные возвращаются в `pending`.
  3. `waiting_docling` старше `docling.max_job_lifetime` отмечаются как `failed` (они не переводятся в `pending`).
  4. Повторно ставить в очередь завершившиеся с ошибкой задачи старше `failed_retry_delay`, пока число попыток меньше `scheduler.max_failed_retries`; исчерпавшие лимит остаются в `failed` с расширенным описанием ошибки.
  5. Удалять завершённые задачи (`done`/`failed`) старше периода хранения (`cleanup_retention`).
