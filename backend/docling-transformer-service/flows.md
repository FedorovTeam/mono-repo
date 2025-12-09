# Flows

## Конвертация DOCX → Markdown через очередь
1. Gateway/Temporal вызывает gRPC метод `ConvertDocumentToMarkdown` с `document_id`, `original_bucket`, `original_object_key`, `filename_extension` и опционально `markdown_bucket`/`markdown_object_key`.
2. gRPC слой валидирует обязательные поля, нормализует расширение файла и проверяет лимит слотов (`service.max_workers`). При переполнении возвращается `ResourceExhausted`.
3. В PostgreSQL добавляется запись в `transformations_queue` со статусом `pending` (поля: `document_id`, `original_bucket/key`, `markdown_bucket/key`, `filename_extension`, `docling_job_id`, `docling_poll_count`). В ответе клиент получает `queue_id`, статус `pending` и путь для Markdown; поля Docling заполняются после первого обращения к внешнему сервису и используются для безопасного продолжения опроса при восстановлении задач.
4. Фоновый воркер с периодом опроса читает по одной задаче `SELECT ... FOR UPDATE SKIP LOCKED`, обновляет статус на `processing` и фиксирует `started_at`.
5. Воркер скачивает исходник из MinIO, вызывает Docling (`POST /v1/convert/file` с заголовками `X-Correlation-Id`, `X-Docling-Source-Filename`, `X-Api-Key`) и сохраняет Markdown в MinIO в заданный `markdown_bucket/object_key`.
6. После успешной загрузки статус в очереди меняется на `done` с заполнением `finished_at`. При ошибке конвертации/загрузки — статус `failed` и текст ошибки в поле `error`.

## Управление конфигурацией
1. При старте `cmd/server` читает `config.yaml` (или путь из `CONFIG_PATH`).
2. Подключения к Docling, MinIO и PostgreSQL берут параметры из конфига, без `.env`.
3. Любые изменения конфига требуют перезапуска сервера; формат задокументирован в README.
