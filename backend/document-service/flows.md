# Бизнес-потоки

1. **Создание документа**
   - Клиент вызывает `CreateDocument` с именем и информацией о бакетах.
   - Сервис генерирует ключи MinIO и создаёт запись в `documents` и `document_versions`.
   - Статус фиксируется как `Uploaded`, добавляется запись в `document_status_logs`.
   - Workflow не запускается до получения события `ConfirmUpload` от storage-service.

2. **Обновление статуса**
   - Клиент вызывает `UpdateDocumentStatus` с целевым статусом.
   - Сервис проверяет допустимый переход (`Uploaded → StoredOriginal → ConvertedToMarkdown → PublishedToGitHub`) или `Failed`.
   - При корректном шаге пишет лог и обновляет статус документа.

3. **Подтверждение загрузки**
   - Storage-service вызывает `ConfirmUpload(document_id, bucket, object_key)` после уведомления MinIO.
   - Document-service фиксирует статус `StoredOriginal`, сохраняет workflow-run-id и запускает Temporal workflow.

3. **Чтение документа**
   - `GetDocument` возвращает документ с последней версией и текущим статусом.

4. **Список**
   - `ListDocuments` выдаёт пагинированный список для отображения прогресса обработки.
