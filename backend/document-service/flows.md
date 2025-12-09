# Бизнес-потоки

1. **Создание документа**
   - Клиент вызывает `CreateDocument` с именем файла; `id` может быть передан, при отсутствии генерируется UUID.
   - Если не заданы владелец, репозиторий, ветка или base-branch, подставляются значения из конфигурации; пустой `base_branch` дополнительно заменяется на текущую ветку.
   - Загружаемый и markdown-бакеты берутся из запроса либо из конфигурации; при отсутствии любого из них возвращается ошибка. Путь для публикации (`publish_path`) и `path` по умолчанию равны имени файла.
- Генератор ключей MinIO проверяет существование бакетов и строит базовый путь `<prefix>/year=YYYY/month=MM/day=DD/<document_id>/<uuid>[.<ext>]`, где `uuid` генерируется случайно, а расширение берется из исходного имени файла (если оно передано). Итоговые ключи — `<base>/original` для исходника и сам `<base>` для Markdown; переданные `upload_object_key`/`markdown_object_key` могут их переопределить.
   - В PostgreSQL создаётся запись с `status=Uploaded`, `workflow_id=document_id`, флагом `create_pr`, версия `1` фиксируется в `document_versions`, в `document_status_logs` добавляется переход в `Uploaded`. Temporal workflow на этом шаге не запускается.

2. **Подтверждение загрузки**
   - Storage-service вызывает `ConfirmUpload(document_id, bucket, object_key)` после уведомления от MinIO.
   - Сервис валидирует `document_id`, совпадение бакета и ключа с сохранёнными значениями. Если Temporal не сконфигурирован, возвращает документ без запуска обработки.
   - При наличии Temporal запускается `DocumentProcessingWorkflow` с идентификатором `workflow_id=document_id` в указанной очереди. В случае ошибки старта статус переводится в `Failed`; при успехе в `documents.workflow_id` сохраняется идентификатор запущенного workflow.
   - Фоновый ретраер раз в заданный интервал просматривает документы в статусе `Failed` с причиной, начинающейся на `start workflow:`. Если количество неудачных запусков меньше лимита, он логирует `correlation_id=workflow_id`, повторно инициирует `DocumentProcessingWorkflow` через Temporal client и сохраняет новый `workflow_id`.

3. **Workflow обработки**
   - Для всех активностей установлены таймауты `2m` и retry-политика Temporal.
   - Перед каждой активностью workflow читает текущее состояние документа из PostgreSQL: если зафиксированы статусы `StoredOriginal` / `ConvertedToMarkdown` / `PublishedToGitHub` (или соответствующие версии), шаг пропускается без повторного обращения к внешнему сервису. Это позволяет безопасно перезапускать workflow после сбоев в публикации/конвертации и при ручном `retryProcessing`.
   - `AwaitOriginal` — запрашивает исходный объект через storage-service (`GetObject`) только при статусе `Uploaded`. При ошибке выставляет `Failed`; при успехе фиксирует статус `StoredOriginal` и версию (берёт последнюю сохранённую, минимум `1`).
   - `ConvertToMarkdown` — вызывает docling-transformer-service, на ошибке записывает `Failed`. При успехе обновляет статус `ConvertedToMarkdown`, сохраняет `MarkdownObjectKey` из ответа и версию `previous+1`; при повторном запуске после удачной конвертации сразу возвращает сохранённый `MarkdownObjectKey`.
   - `PublishMarkdown` — подставляет owner/repo/branch/base-branch из документа или конфигурации (пустой base-branch заменяет на ветку), проверяет доступность репозитория через GitHub API. Требует непустые bucket/object_key Markdown. Commit message берётся из имени документа или пути. При успехе фиксирует `PublishedToGitHub` с версией `previous+1`, при ошибке/пустом ответе — `Failed` с причиной; при наличии статуса `PublishedToGitHub` шаг пропускается.

4. **Ручное обновление статуса**
   - Клиент вызывает `UpdateDocumentStatus`. Допустимы переходы `Uploaded → StoredOriginal → ConvertedToMarkdown → PublishedToGitHub`, повторное выставление текущего статуса, либо перевод в `Failed` (если `error_reason` не задан, используется `reason`).
   - Переданные `version`, `markdown_object_key` или `original_object_key` создают/обновляют запись `document_versions`, при необходимости обновляют ключи в документе. Лог статуса пишется при смене статуса, добавлении версии или переданном `reason`.

5. **Чтение и список**
   - `GetDocument` возвращает документ с актуальными версиями и статусом.
   - `ListDocuments` выдаёт список в порядке создания с пагинацией и опциональным фильтром по статусу.
