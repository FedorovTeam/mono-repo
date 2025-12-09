# Бизнес-потоки

1. **UploadOriginal**
   - gRPC-клиент передает org_slug, project_slug (или оставляет пустыми, тогда берутся значения из входящих метаданных
     `org-slug` и `project-slug`, а при их отсутствии используются дефолты `knowledge`/`documents`), document_id, расширение и
     байты файла.
   - Service валидирует наличие document_id, расширения и содержимого, генерирует ключ по шаблону
     `org/<org_slug>/project/<project_slug>/year=YYYY/month=MM/day=DD/<document_id>.<ext>` и пишет объект в приватный бакет
     `knowledge-original` (или иной бакет из конфигурации, если он указан в секции `minio.buckets.knowledge_docx`).
   - Возвращается bucket/key и timestamp загрузки; в заголовок ответа добавляются `x-correlation-id`, `org-slug` и
     `project-slug` из запроса/метаданных.
   - При старте сервиса настраивается webhook/queue уведомление MinIO (ObjectCreated) для бакета загрузки, выбранного через
     `events.upload_bucket` (по умолчанию `knowledge-original`), используя ARN из `events.webhook_arn`.

2. **UploadMarkdown**
   - После конвертации docling-трансформером вызывается загрузка Markdown.
   - org_slug и project_slug берутся из запроса, gRPC-метаданных или дефолтов аналогично UploadOriginal.
   - Файл пишется в бакет `knowledge-md` (или из конфигурации `minio.buckets.knowledge_md`) с тем же ключом и расширением `.md`.
   - В ответе возвращаются bucket/key, временная метка и те же служебные заголовки (`x-correlation-id`, `org-slug`,
     `project-slug`).

3. **GetObject**
   - Клиент запрашивает bucket + object_key, сервис проверяет доступность бакета и отдает содержимое из MinIO.
   - В ответе передаются служебные заголовки с `x-correlation-id`, `org-slug` и `project-slug` (значения берутся из
     входящих метаданных или дефолтов сервера).

4. **GenerateUploadURL**
   - Клиент запрашивает подписанную PUT-ссылку, передавая document_id, file_name, content_length, expires_in_seconds и
     опциональный correlation_id.
   - Сервис валидирует параметры, проверяет, что расширение файла входит в список `doc`, `docx`, `pdf`, и убеждается в
     доступности бакета загрузки (`events.upload_bucket` или `knowledge-original`).
   - Генерируется ключ по стандартному шаблону и подписывается URL MinIO с заголовком `x-amz-meta-correlation-id` для
     сохранения трассировки загрузки.
   - В ответе возвращаются upload_url, bucket, object_key и время жизни ссылки, а также служебные заголовки с correlation_id и
     slug’ами по умолчанию (`knowledge`/`documents`).

Все операции используют приватные бакеты MinIO; логирование запросов реализовано через серверные интерцепторы gRPC.
