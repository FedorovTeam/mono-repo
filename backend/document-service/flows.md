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

3. **Подтверждение загрузки и обработка**
   - Storage-service вызывает `ConfirmUpload(document_id, bucket, object_key)` после уведомления MinIO.
   - Document-service валидирует бакет/ключ, сохраняет RunID workflow и запускает Temporal workflow с `workflow_id = document_id`.
   - Workflow включает активности:
     - `AwaitOriginal` — опрашивает storage-service (`GetObject`) до готовности исходника, после чего выставляет статус `StoredOriginal` в PostgreSQL.
     - `ConvertToMarkdown` — вызывает docling-transformer-service и записывает `ConvertedToMarkdown` с ключом Markdown.
     - `PublishMarkdown` — вызывает github-publisher-service по gRPC, передавая `bucket/object_key` Markdown из MinIO и `correlation-id = workflow_id`; commit message берётся из имени документа, флаг `create_pr` прокидывается как есть.
   - Ответ github-publisher-service с `commit_sha`/`pull_request_*` переводит статус в `PublishedToGitHub`, отсутствие валидного ответа или ошибка меняют статус на `Failed` c заполненным `error_reason`.
   - При ошибке любая активность пишет статус `Failed` и причину в `documents.error_reason`, Temporal применяет retry-политику для временных сбоев.
   - Повторная загрузка исходника с тем же `document_id` (повторный `ConfirmUpload`) перезапускает workflow и даёт новый цикл статусов.

3. **Чтение документа**
   - `GetDocument` возвращает документ с последней версией и текущим статусом.

4. **Список**
   - `ListDocuments` выдаёт пагинированный список для отображения прогресса обработки.
