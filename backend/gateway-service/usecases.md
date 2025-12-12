# usecases

## Загрузка оригинала через подписанную ссылку
1. Клиент добавляет `X-API-Key` (обязательно для всех GraphQL запросов, включая чтение статусов; при необходимости — `X-Correlation-ID`) и вызывает мутацию `uploadKnowledgeDocument` с параметрами `fileName`, `fileSizeBytes`, `repository`, `branch`, `path`. `repository` можно передавать как `<repo>` или `<owner>/<repo>` — gateway сохранит владельца отдельно.
2. gateway валидирует расширение, размер и обязательные поля по настройкам `uploads.*`, генерирует новый `document_id` и correlation-id (если его не было в запросе), запрашивает presigned PUT URL через `storage-service.GenerateUploadURL` и создаёт запись в `document-service.CreateDocument`, передавая bucket/object ключ из ответа хранилища.
3. В ответе GraphQL возвращаются `uploadUrl` + `uploadMethod` (всегда `PUT`) и объект документа со статусом `UPLOADED`, `repositoryOwner` (если указан), `jobId` (workflowId/Id документа на стороне document-service маппится в это поле) и переданными метаданными. Correlation-id дублируется в заголовке ответа.
4. Клиент загружает файл по `uploadUrl` методом `PUT`; после события `s3:ObjectCreated:*` MinIO отправляет webhook на `/internal/storage/uploaded`, который подтверждает загрузку через `document-service.ConfirmUpload`. Документ определяется по имени объекта (часть до расширения), correlation-id восстанавливается из метаданных `x-amz-meta-correlation-id` или сгенерируется заново, при успешной обработке HTTP-ответ — `202 Accepted`.

## Получение статуса/списка документов
1. С авторизацией по `X-API-Key` выполняется запрос `knowledgeDocument(id)` или `knowledgeDocuments(filter, pagination)`.
2. gateway пробрасывает `correlation-id` в gRPC-запросы `document-service`, мапит статусы (`UPLOADED/ STORED_ORIGINAL/ CONVERTED_TO_MARKDOWN/ PUBLISHED_TO_GITHUB/ FAILED`) и возвращает найденный документ или список с `totalCount` (pagination по `limit/offset`, по умолчанию 10/0, фильтр по статусу и репозиторию).

## Повторная обработка после ошибки
1. При получении статуса `FAILED` клиент вызывает мутацию `retryProcessing(documentId)` с `X-API-Key`.
2. gateway прокидывает вызов в `document-service.RetryProcessing`, обеспечивая `correlation-id`, и возвращает обновлённый объект документа.

