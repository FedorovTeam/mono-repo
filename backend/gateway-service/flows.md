# flows

## Upload → Convert → Publish
1. Клиент выполняет GraphQL-мутацию `uploadKnowledgeDocument` с `fileName`, `fileSizeBytes`, целевыми `repository`, `branch`, `path`, передавая `X-API-Key` (обязателен для всех GraphQL эндпоинтов, включая `/query` и `/playground`) и опционально `X-Correlation-ID`.
2. Middleware и резолвер извлекают или создают `correlation-id`, прикладывают его к ответу и валидируют только обязательные поля (`fileName`, `repository`, `branch`, `path`) и положительный `fileSizeBytes`.
3. Gateway генерирует `documentId` (UUID) и запрашивает presigned ссылку через gRPC `storage-service.GenerateUploadURL`, передавая `documentId`, `correlation-id`, `fileName`, размер и TTL из `uploads.signedUrlTtlSeconds`. В ответе используются `uploadUrl`, `bucket`/`objectKey`, а также `markdownBucket`/`markdownObjectKey` для конвертированной версии.
4. После получения ссылки создаётся запись через gRPC `document-service.CreateDocument` с `id`, путём, размером, `correlation-id`, исходным `repository` (без нормализации и владельца), а также `uploadBucket`/`uploadObjectKey` и `markdownBucket`/`markdownObjectKey` из ответа storage-service. Клиент получает `documentId` и текущий статус; дальнейшие шаги выполняются на стороне document-service.
5. Мутация отвечает GraphQL-пейлоадом с сущностью `document`, полями `uploadUrl` и `uploadMethod = PUT`.
6. Клиент загружает файл по `uploadUrl` (HTTP PUT). После события `s3:ObjectCreated:*` MinIO отправляет webhook на `/internal/storage/uploaded`.
7. Webhook-драйвер gateway обрабатывает все записи из уведомления: декодирует `objectKey`, извлекает `documentId` из имени файла и пробует достать `correlation-id` из `x-amz-meta-correlation-id` в user metadata/response elements. Если `correlation-id` отсутствует, генерируется новый. Для каждой записи выполняется gRPC-вызов `document-service.ConfirmUpload` с bucket/objectKey/`correlation-id`. Если ни одна запись не обработана, возвращается 400, иначе 202 Accepted.
## Получение статуса
1. Запросы `knowledgeDocument` и `knowledgeDocuments` принимаются только с `X-API-Key`: middleware проверяет ключ, добавляет/пробрасывает `x-correlation-id`, а gRPC-клиенты передают оба заголовка в metadata downstream-сервисам.
2. Поддерживаются фильтры по статусу и репозиторию, а также пагинация (`limit`/`offset`, по умолчанию `limit = 10`, `offset = 0`).
3. Логи, метрики и трейсинг уходят в OTLP collector; заголовок `X-Correlation-ID` возвращается клиенту.
4. Ответы мапятся в GraphQL-тип `KnowledgeDocument` (статус, id, ошибка) и возвращаются клиенту; идентификатор используется downstream-сервисами для дальнейшей обработки документа.
