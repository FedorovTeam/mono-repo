# Бизнес-потоки

1. **UploadOriginal**
   - gRPC-клиент передает org_slug, project_slug (или оставляет пустыми, тогда берутся значения из входящих метаданных
     `org-slug` и `project-slug`, а при их отсутствии используются дефолты `knowledge`/`documents`), document_id, расширение и
     байты файла.
   - Service валидирует наличие document_id, расширения и содержимого, убеждается в доступности бакета загрузки и генерирует
     ключ по шаблону `org/<org_slug>/project/<project_slug>/year=YYYY/month=MM/day=DD/<uuid>/<document_id>.<ext>`, после чего
     пишет объект в приватный бакет `knowledge-original` (или иной бакет из конфигурации `minio.buckets.knowledge_docx`).
   - correlation_id можно передать прямо в запросе или через gRPC-метаданные `x-correlation-id`; это значение возвращается в
     заголовке ответа (дублируется как `x-amz-meta-correlation-id` при наличии) вместе с `org-slug` и `project-slug`.
   - При старте сервиса настраивается webhook/queue уведомление MinIO (ObjectCreated) для бакета загрузки, выбранного через
     `events.upload_bucket` (по умолчанию `knowledge-original`), используя ARN из `events.webhook_arn`.

2. **UploadMarkdown**
   - После конвертации docling-трансформером вызывается загрузка Markdown.
   - org_slug и project_slug берутся из запроса, gRPC-метаданных или дефолтов аналогично UploadOriginal.
   - Перед записью проверяется доступность бакета `knowledge-md` (или заданного в `minio.buckets.knowledge_md`); файл пишется с
     тем же ключом и расширением `.md`.
   - correlation_id также можно передать полем запроса или в `x-correlation-id`; ответ содержит bucket/key, временную метку и
     служебные заголовки (`x-correlation-id`, `x-amz-meta-correlation-id`, `org-slug`, `project-slug`).

3. **GetObject**
   - Клиент запрашивает bucket + object_key, сервис проверяет доступность указанного бакета и отдает содержимое из MinIO.
   - В ответе передаются служебные заголовки с `x-correlation-id`, `org-slug` и `project-slug` (значения берутся из
     входящих метаданных или дефолтов сервера; отдельного поля correlation_id в запросе нет).

4. **GenerateUploadURL**
   - Клиент запрашивает подписанную PUT-ссылку, передавая document_id, file_name, content_length, expires_in_seconds и
     опциональный correlation_id (можно также прокинуть через метаданные `x-correlation-id`).
   - Сервис валидирует параметры (document_id/file_name обязательны, content_length должен быть > 0 и <= `uploads.max_size_bytes`,
     expires_in_seconds должен быть > 0 или заменяется на `uploads.signed_url_ttl_seconds`), проверяет расширение на вхождение в
     `uploads.allowed_extensions` и убеждается в доступности бакета загрузки (`events.upload_bucket` или `knowledge-original`) и
     markdown-бакета (`minio.buckets.knowledge_md`).
   - org_slug и project_slug берутся из дефолтов сервера (`knowledge`/`documents`); в подписанную ссылку добавляется заголовок
     `x-amz-meta-correlation-id` при наличии correlation_id, а ключи для исходника и Markdown формируются в одном и том же пути.
   - В ответе возвращаются upload_url, bucket, object_key, markdown_bucket/markdown_object_key и фактическое время жизни ссылки, а также
     служебные заголовки с correlation_id и дефолтными slug’ами (`knowledge`/`documents`).

5. **Обработка задач store_original**
   - При запуске поднимается фоновой воркер, который с периодом `processing.poll_interval` (по умолчанию 5 секунд) достает из БД
     `processing_jobs` задачу со stage из конфигурации (по умолчанию `store_original`) и статусом pending.
   - Перед выполнением задача переводится в in_progress и логируется в `job_events`; после успешного шага документ переводится в
     статус `StoredOriginal`, добавляется запись в `document_status_logs` (workflow_id берется из payload["correlation_id"]).
   - При ошибках срабатывает пересчет: попытки инкрементируются до `processing.max_attempts` (по умолчанию 3), следующий запуск
     откладывается на `attempt * processing.backoff` (по умолчанию 5 секунд), в payload сохраняется last_error; после исчерпания
     попыток статус задачи становится failed.

Все операции используют приватные бакеты MinIO; логирование запросов реализовано через серверные интерцепторы gRPC.
