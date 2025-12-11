# Use-cases

## 1. Загрузка оригинала от gateway-service
- gateway-service принимает исходный файл (doc/docx/pdf) и вызывает `UploadOriginal`.
- `org_slug` / `project_slug` берутся из gRPC-метаданных (`org-slug`, `project-slug`) или из тела запроса, по умолчанию `knowledge/documents`.
- Обязательные поля: `document_id`, `filename_extension` (без точки) и `content`.
- Ключ формируется по шаблону `org/<org_slug>/project/<project_slug>/year=YYYY/month=MM/day=DD/<uuid>/<document_id>.<ext>` и кладётся в приватный бакет `knowledge-original`.
- В ответ возвращаются `bucket`/`object_key` и выставляется `x-correlation-id` в gRPC-хедерах для трассировки.

## 2. Сохранение Markdown после конвертации
- docling-transformer-service возвращает Markdown для документа.
- document-service отправляет `UploadMarkdown` с тем же `document_id`; `org_slug` и `project_slug` резолвятся как в оригинале.
- Обязательные поля: `document_id` и `content`.
- Markdown сохраняется в приватный бакет `knowledge-md` по тому же шаблону ключей; ответ содержит `bucket` и `object_key` для связи с записью документа и дальнейшей публикации.
- В ответе так же дублируются `x-correlation-id`, `org-slug`, `project-slug` в gRPC-хедерах.

## 3. Повторная выдача файла для отладки
- Администратор или внутренний сервис запрашивает `GetObject` с `bucket` и `object_key`.
- Возвращаются байты исходника или Markdown; в ответе проставляются служебные метаданные (`x-correlation-id`, `org-slug`, `project-slug`).

## 4. Предоставление presigned PUT для прямой загрузки
- Для загрузки напрямую в MinIO вызывается `GenerateUploadURL`.
- Разрешены только расширения `doc`, `docx`, `pdf`; `content_length` и `expires_in_seconds` обязаны быть больше нуля.
- Ключ строится с сервисными значениями `org_slug=knowledge` и `project_slug=documents` и кладётся в бакет из конфигурации `events.upload_bucket` (по умолчанию `knowledge-original`).
- Если передан `correlation_id` (в теле запроса или в `x-correlation-id`), он добавляется в `x-amz-meta-correlation-id` для загрузки и дублируется в gRPC-хедерах ответа вместе с `org-slug`/`project-slug`.
- В ответе возвращаются `upload_url`, `bucket`, `object_key` и `expires_in_seconds` для дальнейшей загрузки.

## 5. Настройка уведомлений о загрузке
- При старте сервис проверяет наличие бакетов (`knowledge-original`, `knowledge-md` и `events.upload_bucket`, если отличается) и настраивает MinIO webhook/queue (`events.webhook_arn`) для бакета `events.upload_bucket`; отсутствие ARN считается ошибкой и приводит к падению запуска.
- Включаются события `s3:ObjectCreated:*`, чтобы внешние подписчики получали уведомление после успешной загрузки оригиналов.

## 6. Подтверждение загрузки через DocumentService
- Для обработчиков очереди/вебхука предусмотрен `UploadEventHandler`: он подписывается на события `s3:ObjectCreated:*` и на каждый ключ типа `<document_id>.<ext>` отправляет `ConfirmUpload` в document-service.
- Если из имени файла не удаётся извлечь `document_id`, уведомление пропускается, а ошибки вызова подтверждения логируются.
