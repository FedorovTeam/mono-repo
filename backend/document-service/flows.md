# Бизнес-потоки

1. **Создание документа**
   - Клиент вызывает `CreateDocument` с именем файла; `id` может быть передан, при отсутствии генерируется UUID.
   - Если не заданы владелец, репозиторий, ветка или base-branch, подставляются значения из конфигурации; пустой `base_branch` дополнительно заменяется на текущую ветку.
   - Загружаемый и markdown-бакеты/ключи приходят из `storage-service` (через GenerateUploadURL или прямые `Upload*` gRPC-вызовы) или из запроса; при отсутствии любого обязательного значения возвращается ошибка. Путь для публикации (`publish_path`) и `path` по умолчанию равны имени файла.
   - Репозиторий можно передать в виде `owner/repo` — в этом случае владелец подтягивается из префикса. Наличие лишних слэшей или несовпадение owner/repo приводит к ошибке валидации.
   - В PostgreSQL создаётся запись с `status=Uploaded`, `workflow_id=document_id`, флагом `create_pr`, версия `1` фиксируется в `document_versions`, в `document_status_logs` добавляется переход в `Uploaded`.

2. **Подтверждение загрузки**
   - Storage-service вызывает `ConfirmUpload(document_id, bucket, object_key)` после успешного `UploadOriginal`/webhook-а из MinIO.
   - Сервис валидирует `document_id`, совпадение бакета и ключа с сохранёнными значениями. При успехе документ переводится в `StoredOriginal` и заводит задачу `processing_jobs` со стадией `store_original` в статусе `pending`; при ошибке документ переводится в `Failed`.
   - Периодический cron-воркер `UploadReconciler` опрашивает таблицу `documents` в статусе `Uploaded` и повторно вызывает `ConfirmUpload`, не проверяя объект напрямую: реальную проверку выполняет storage-service.

3. **Обработка по статусам**
   - Цепочка построена на таблице `processing_jobs`. Исполнители стадий (`storage-service`, `docling-transformer-service`, `github-publisher-service`) вызываются через gRPC и меняют статус задачи (`pending` → `in_progress` → `completed|failed`).
   - Воркер `JobFollower` внутри `document-service` каждые N секунд читает готовые задачи (`completed`/`failed`) и решает переход:
     - `store_original` → создаёт новую задачу `convert_to_markdown` с payload из документа (bucket/object_key, correlation_id).
     - `convert_to_markdown` → создаёт `publish_to_github` с ссылками на Markdown-объект и параметрами публикации.
     - Любая стадия в статусе `failed` → переводит документ в `Failed`, причина берётся из `payload.last_error`.
   - Стадии можно повторять: если исполнитель вернул задачу в `pending` (retry), `JobFollower` её не трогает до окончательного `completed`/`failed`.
   - Воркеры `convert_to_markdown` и `publish_to_github` больше не содержат бизнес-логики интеграций: они вызывают gRPC клиентов соседних сервисов, фиксируют `queue_id`/`commit_sha` и обновляют статусы в PostgreSQL, повторно планируя задачу, если внешний сервис ещё работает.

4. **Ручное обновление статуса**
   - Клиент вызывает `UpdateDocumentStatus`. Допустимы переходы `Uploaded → StoredOriginal → ConvertedToMarkdown → PublishedToGitHub`, повторное выставление текущего статуса, либо перевод в `Failed` (если `error_reason` не задан, используется `reason`).
   - Переданные `version`, `markdown_object_key` или `original_object_key` создают/обновляют запись `document_versions`, при необходимости обновляют ключи в документе. Лог статуса пишется при смене статуса, добавлении версии или переданном `reason`.

5. **Чтение и список**
   - `GetDocument` возвращает документ с актуальными версиями и статусом.
   - `ListDocuments` выдаёт список в порядке `created_at DESC` с пагинацией и опциональным фильтром по статусу.
