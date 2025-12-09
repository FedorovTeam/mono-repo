# Сценарии использования

1. **Регистрация документа и получение ключей для загрузки**
   - Клиент вызывает `CreateDocument`, передавая имя файла и (опционально) идентификатор, владельца, репозиторий, ветки, путь публикации и собственные бакеты/ключи.
   - Сервис валидирует имя и UUID, подставляет значения по умолчанию для владельца, репозитория и веток, а также использует `KeyManager` для генерации путей в MinIO. В ответе возвращаются `upload_bucket`/`upload_object_key` и `markdown_bucket`/`markdown_object_key` для выгрузки файлов, статус выставляется в `Uploaded`.

2. **Загрузка файла и запуск автоматической обработки**
   - Клиент загружает оригинал в MinIO, используя выданные `upload_bucket` и `upload_object_key`.
   - После успешной загрузки вызывается `ConfirmUpload`. Сервис проверяет соответствие бакета и ключа ожиданиям и запускает Temporal‑воркфлоу `DocumentProcessingWorkflow` (если Temporal настроен), сохраняя `workflow_id` в документе.

3. **Пошаговая обработка Temporal‑воркфлоу**
   - Activity `AwaitOriginal` запрашивает наличие объекта в MinIO через `StorageService.GetObject` и обновляет статус на `StoredOriginal` (версия берётся из последней сохранённой записи).
   - Activity `ConvertToMarkdown` вызывает `TransformerService.ConvertDocxToMarkdown`, сохраняет новый `markdown_object_key` (если пришёл другой ключ) и выставляет статус `ConvertedToMarkdown`.
   - Activity `PublishMarkdown` публикует markdown в GitHub через `GithubPublisherService.PublishMarkdown`, используя owner/repository/branch/baseBranch/path из документа и опцию `create_pr`. При успешном коммите статус становится `PublishedToGitHub`.

4. **Ручное обновление статуса**
   - Любой внешний шаг может вызвать `UpdateDocumentStatus`, передав желаемый статус, причину и, при необходимости, `version`, `original_object_key` или `markdown_object_key`. Переходы валидируются: допускаются только последовательные изменения (`Uploaded → StoredOriginal → ConvertedToMarkdown → PublishedToGitHub`) либо перевод в `Failed` из любого состояния.
   - При установке `Failed` поле `error_reason` заполняется переданным текстом.

5. **Получение состояния**
   - `GetDocument` возвращает текущее состояние документа с ключами, версиями и ошибкой (если есть).
   - `ListDocuments` позволяет получить список документов с пагинацией и фильтрацией по статусу.

6. **Обработка ошибок в воркфлоу**
   - Если любой шаг `AwaitOriginal`, `ConvertToMarkdown` или `PublishMarkdown` завершается ошибкой, сервис вызывает `UpdateStatus` с `Failed` и текстом причины. В этом случае дальнейшие шаги воркфлоу не выполняются.
