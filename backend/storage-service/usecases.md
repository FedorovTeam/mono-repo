# Use-cases

## 1. Загрузка оригинала от gateway-service
- gateway-service передает исходный файл (doc/docx/pdf) в метод `UploadOriginal`.
- `org_slug` и `project_slug` резолвятся из gRPC-метаданных (`org-slug`, `project-slug`), значений запроса или берут дефолты `knowledge`/`documents`.
- Обязательные поля: `document_id`, `filename_extension` (без точки) и непустой `content`; при отсутствии полей gRPC возвращает `INVALID_ARGUMENT`.
- Ключ формируется по шаблону `org/<org_slug>/project/<project_slug>/year=YYYY/month=MM/day=DD/<uuid>/<document_id>.<ext>` и кладётся в приватный бакет `minio.buckets.knowledge_docx`.
- В ответ возвращаются `bucket`/`object_key`, а в заголовки ответа добавляются `x-correlation-id`, `org-slug`, `project-slug` и `x-amz-meta-correlation-id` (если корреляция указана в запросе или метаданных).

## 2. Сохранение Markdown после конвертации
- docling-transformer-service возвращает Markdown, после чего document-service вызывает `UploadMarkdown` с тем же `document_id`.
- `org_slug`/`project_slug` обрабатываются так же, как при загрузке оригинала; обязательны `document_id` и непустой `content`.
- Markdown сохраняется в бакет `minio.buckets.knowledge_md` по тому же шаблону ключей (расширение всегда `.md`).
- Ответ содержит `bucket` и `object_key`, а также заголовки `x-correlation-id`/`org-slug`/`project-slug`/`x-amz-meta-correlation-id` по корреляции запроса.

## 3. Повторная выдача файла для отладки
- Внутренний клиент вызывает `GetObject` с `bucket` и `object_key` для оригинала или Markdown.
- Возвращаются байты объекта; в ответ добавляются служебные заголовки `x-correlation-id`, `org-slug`, `project-slug`.
- Запросы с пустым `bucket` или `object_key` отклоняются с `INVALID_ARGUMENT`.

## 4. Предоставление presigned PUT для прямой загрузки
- Для прямой загрузки в MinIO вызывается `GenerateUploadURL`.
- Обязательные поля: `document_id`, `file_name`, `content_length>0`; допускаются только расширения из `uploads.allowed_extensions` (по умолчанию `doc`, `docx`, `pdf`) и размер не больше `uploads.max_size_bytes`.
- Ключ строится с сервисными значениями `org_slug=knowledge` и `project_slug=documents` и кладётся в бакет `events.upload_bucket` (если он не задан — `minio.buckets.knowledge_docx`); дополнительно проверяется доступность Markdown-бакета.
- Если передан `correlation_id` (в теле запроса или в `x-correlation-id`), он добавляется в `x-amz-meta-correlation-id` подписанной ссылки и дублируется в ответных gRPC-хедерах.
- В ответе возвращаются `upload_url`, `bucket`, `object_key`, `markdown_bucket`, `markdown_object_key` и `expires_in_seconds` для последующей загрузки и привязки Markdown-файла.

## 5. Инициализация бакетов и уведомлений при старте
- При запуске сервис создаёт/проверяет приватные бакеты `minio.buckets.knowledge_docx`, `minio.buckets.knowledge_md` и, при отличиях, `events.upload_bucket`.
- Затем настраивает webhook/очередь MinIO для бакета загрузки через `events.webhook_arn`; отсутствие ARN или ошибки конфигурации приводят к остановке запуска.

## 6. Фоновая фиксация статуса сохранения
- Воркеры читают таблицу `processing_jobs` для стадии `processing.stage` (по умолчанию `store_original`) и берут задачи со статусом `pending`.
- Для каждой задачи статус обновляется на `in_progress`, а в таблицу документов записывается состояние `StoredOriginal` с причиной «processed by storage worker»; при неудаче задача переносится с увеличением `attempts` и `next_run_at`.
- Успешно обработанные задачи получают статус `completed`; все изменения фиксируются в `job_events` и `document_status_logs`.
