# Бизнес-потоки

1. **Создание документа**
   - Клиент вызывает `CreateDocument` с именем файла; `id` может быть передан, при отсутствии генерируется UUID.
   - Если не заданы владелец, репозиторий, ветка или base-branch, подставляются значения из конфигурации; пустой `base_branch` дополнительно заменяется на текущую ветку. Репозиторий разрешается передать в виде `owner/repo` — владелец берётся из префикса, лишние слэши или несовпадение owner/repo приводят к ошибке.
   - `upload_bucket`/`upload_object_key` и `markdown_bucket`/`markdown_object_key` обязательны: сервис валидирует непустые значения и сохраняет их как есть, не создавая ключи и не вызывая `storage-service` в этом методе. Путь публикации (`publish_path`) и `path` по умолчанию равны имени файла.
   - В PostgreSQL создаётся запись с `status=Uploaded`, `workflow_id=document_id`, флагом `create_pr`, версия `1` фиксируется в `document_versions`, в `document_status_logs` добавляется переход в `Uploaded`.

2. **Подтверждение загрузки**
   - Storage-service вызывает `ConfirmUpload(document_id, bucket, object_key)` после успешного `UploadOriginal`/webhook-а из MinIO.
   - Сервис валидирует `document_id`, совпадение бакета и ключа с сохранёнными значениями и проверяет наличие объекта через `storage-service.GetObject`. При успехе создаётся задача `processing_jobs` со стадией `store_original` в статусе `pending`; статус документа пока остаётся `Uploaded`, ошибка постановки задачи переводит его в `Failed`.
   - Периодический cron-воркер `UploadReconciler` опрашивает таблицу `documents` в статусе `Uploaded` и повторно вызывает `ConfirmUpload`, используя сохранённые bucket/object_key; проверка объекта проходит через gRPC storage-service.

3. **Обработка по статусам**
   - Цепочка построена на таблице `processing_jobs`. Stage worker обрабатывает pending-задачи по стадиям, меняя статус задачи на `completed` или `failed`, а JobFollower реагирует на завершённые задачи, планируя следующую стадию или фиксируя ошибку документа.
   - `store_original` выполняется внутри stage worker: он проставляет документу статус `StoredOriginal` и закрывает задачу.
   - `JobFollower` каждую итерацию читает `completed`/`failed` задачи. Для `store_original` он ставит `convert_to_markdown` с подготовленным payload (bucket/object_key, correlation_id), для `convert_to_markdown` — `publish_to_github` с параметрами публикации. Если задача упала, JobFollower помечает документ `Failed`, используя `payload.last_error`.
  - Для `convert_to_markdown` stage worker один раз вызывает gRPC `ConvertDocumentToMarkdown` с флагом ожидания результата. Transformer-service сам управляет очередью, Docling и повторными попытками, возвращая финальный `queue_id` и итоговый `markdown_object_key`; stage worker фиксирует версию и переводит документ в `ConvertedToMarkdown`.
   - Для `publish_to_github` stage worker собирает payload из сохранённых bucket/object_key, owner/repository, branch/base_branch, пути и флага `create_pr`, добавляет `idempotency_key`, `attempt` и `retry_policy`, вызывает `github-publisher-service` и пишет в `job_events` `commit_sha`, `pull_request_*`, `retry_after`, `idempotency_hit`. Наличие `retry_after` или backoff для публикации переносит `next_run_at`; при успехе документ получает статус `PublishedToGitHub`.

4. **Ручное обновление статуса**
   - Клиент вызывает `UpdateDocumentStatus`. Допустимы переходы `Uploaded → StoredOriginal → ConvertedToMarkdown → PublishedToGitHub`, повторное выставление текущего статуса, либо перевод в `Failed` (если `error_reason` не задан, используется `reason`). Восстановление после ошибки допускает переход из `Failed` в `StoredOriginal`/`ConvertedToMarkdown`/`PublishedToGitHub`.
   - Переданные `version`, `markdown_object_key` или `original_object_key` создают/обновляют запись `document_versions`, при необходимости обновляют ключи в документе. Лог статуса пишется при смене статуса, добавлении версии или переданном `reason`. После перехода в `StoredOriginal` или `ConvertedToMarkdown` сервис сам ставит задачи следующей стадии.

5. **Чтение и список**
   - `GetDocument` возвращает документ с актуальными версиями и статусом.
   - `ListDocuments` выдаёт список в порядке `created_at DESC` с пагинацией и опциональным фильтром по статусу.

6. **Фоновые помощники**
   - `UploadReconciler` периодически ищет документы в статусе `Uploaded`, проверяет возраст записи и повторно вызывает `ConfirmUpload` с сохранёнными bucket/object_key; проверка наличия оригинала происходит внутри `ConfirmUpload` через `storage-service.GetObject`.
   - Cron-воркеры опираются на `processing_jobs` и повторяют попытки согласно счётчикам `attempts` и расписанию `next_run_at`, записывая детали в `job_events`.

7. **Обработка ошибок в воркфлоу**
   - Если любой шаг (`ConfirmUpload` с невалидными ссылками, `ConvertToMarkdown`, `PublishMarkdown`) завершается ошибкой или не находит необходимые данные, stage worker или JobFollower проставляют `Failed` с текстом причины; дальнейшие шаги воркфлоу не выполняются, пока статус не будет восстановлен вручную.
