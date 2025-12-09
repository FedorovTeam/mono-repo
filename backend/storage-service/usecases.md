# Use-cases

## 1. Загрузка оригинала от gateway-service
- gateway-service принимает исходный файл (doc/docx/pdf) и вызывает `UploadOriginal`.
- `org_slug` / `project_slug` берутся из запроса или gRPC метаданных, по умолчанию `knowledge/documents`.
- Ключ формируется по шаблону `org/<org_slug>/project/<project_slug>/year=YYYY/month=MM/day=DD/<document_id>.<ext>` и кладётся в приватный бакет `knowledge-docx`.
- В ответ возвращаются `bucket`/`object_key` и выставляется `x-correlation-id` в gRPC-хедерах для трассировки.

## 2. Сохранение Markdown после конвертации
- docling-transformer-service возвращает Markdown для документа.
- document-service отправляет `UploadMarkdown` с тем же `document_id`; `org_slug` и `project_slug` резолвятся как в оригинале.
- Markdown сохраняется в приватный бакет `knowledge-md` по тому же шаблону ключей; ответ содержит `bucket` и `object_key` для связи с записью документа и дальнейшей публикации.

## 3. Повторная выдача файла для отладки
- Администратор или внутренний сервис запрашивает `GetObject` с `bucket` и `object_key`.
- Возвращаются байты исходника или Markdown; в ответе проставляются служебные метаданные (`x-correlation-id`, `org-slug`, `project-slug`).

## 4. Предоставление presigned PUT для прямой загрузки
- Для загрузки напрямую в MinIO вызывается `GenerateUploadURL`.
- Разрешены только расширения `doc`, `docx`, `pdf`; длина содержимого и срок действия ссылки должны быть больше нуля.
- Ссылка подписывается для бакета из конфигурации `events.upload_bucket` (по умолчанию `knowledge-docx`), ключ строится по стандартному шаблону и содержит `x-amz-meta-correlation-id`.
- В ответе возвращаются `upload_url`, `bucket`, `object_key` и `expires_in_seconds` для дальнейшей загрузки.

## 5. Настройка уведомлений о загрузке
- При старте сервис настраивает MinIO webhook/queue (`events.webhook_arn`) для бакета `events.upload_bucket`.
- Включаются события `s3:ObjectCreated:*`, чтобы gateway-service получал уведомление после успешной загрузки оригиналов.
