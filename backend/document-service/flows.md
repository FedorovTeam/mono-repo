# Бизнес-потоки

1. **Создание документа**
   - Клиент вызывает `CreateDocument` с именем и информацией о бакетах.
   - Если владелец репозитория, имя репозитория или ветка не переданы, сервис подставляет значения по умолчанию из `config.yaml` (`github.owner`, `github.repository`, `github.branch`, `github.base_branch`).
   - Сервис генерирует ключи MinIO с шаблоном `<prefix>/year=YYYY/month=MM/day=DD/<document_id>/<entity_path>` и создаёт запись в `documents` и `document_versions`.
   - Статус фиксируется как `Uploaded`, добавляется запись в `document_status_logs`, поле `workflow_id` резервируется и равно `document_id`.
   - Workflow не запускается до получения события `ConfirmUpload` от storage-service.

2. **Обновление статуса**
   - Клиент вызывает `UpdateDocumentStatus` с целевым статусом.
   - Сервис проверяет допустимый переход (`Uploaded → StoredOriginal → ConvertedToMarkdown → PublishedToGitHub`) или `Failed`.
   - При корректном шаге пишет лог и обновляет статус документа.

3. **Подтверждение загрузки**
   - Storage-service вызывает `ConfirmUpload(document_id, bucket, object_key)` после уведомления MinIO.
   - Document-service фиксирует статус `StoredOriginal`, сохраняет workflow-id/RunID и запускает Temporal workflow с тем же `workflow_id`.
   - В ходе Workflow шаг публикации отправляет `commit_message`, сформированный из имени документа, прокидывает `correlation_id` = workflow-id, передаёт `create_pr` и использует либо содержимое Markdown, либо пару `bucket/object_key` из хранилища, если контент отсутствует.

3. **Чтение документа**
   - `GetDocument` возвращает документ с последней версией и текущим статусом.

4. **Список**
   - `ListDocuments` выдаёт пагинированный список для отображения прогресса обработки.
