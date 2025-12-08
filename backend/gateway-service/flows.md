# flows

## Upload → Convert → Publish
1. Клиент отправляет `uploadKnowledgeDocument` с `fileName`, `fileSizeBytes`, целевым репозиторием и путём.
2. gateway проверяет `X-API-Key`, пробрасывает/создаёт `correlation-id` и запрашивает presigned ссылку через gRPC `storage-service.GenerateUploadURL` (с TTL из `uploads.signedUrlTtlSeconds`).
3. После успешного ответа создаётся запись через gRPC `document-service.CreateDocument` с размером, путём и `correlation-id`. `document-service` возвращает `workflow_id`, заранее выбранный под будущий запуск.
4. Клиент загружает DOCX по выданному `uploadUrl` (HTTP PUT). После подтверждения загрузки `document-service` запускает Temporal workflow и ведёт статусы.
5. gateway возвращает ID, статус и signed URL. Ошибки повторяются через `retryProcessing`.

## Получение статуса
1. Запрос `knowledgeDocument` или `knowledgeDocuments` направляется в backend-document-service с `x-correlation-id`.
2. Логи, метрики и трейсинг уходят в OTLP collector; заголовок `X-Correlation-ID` возвращается клиенту.
3. Ответы мапятся в GraphQL типы и возвращаются клиенту.

