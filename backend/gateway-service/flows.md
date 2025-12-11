# flows

## Upload → Convert → Publish
1. Клиент выполняет GraphQL-мутацию `uploadKnowledgeDocument` с `fileName`, `fileSizeBytes`, целевыми `repository`, `branch`, `path`. Репозиторий может быть передан как `<repo>` или `<owner>/<repo>` — gateway выделяет владельца и нормализует имя репозитория.
2. Доступ к GraphQL защищён `X-API-Key`; gateway проверяет ключ, извлекает/создаёт `correlation-id` и валидирует вход (обязательные поля, `fileSizeBytes`, допустимые расширения, лимит `uploads.maxSizeBytes`).
3. Gateway генерирует `documentId` (UUID) и запрашивает presigned ссылку через gRPC `storage-service.GenerateUploadURL`, передавая `documentId`, `correlation-id`, `fileName`, размер и TTL из `uploads.signedUrlTtlSeconds`. В ответе приходят `bucket`/`objectKey`, которые используются дальше.
4. После получения ссылки создаётся запись через gRPC `document-service.CreateDocument` с `id`, путём, размером, `correlation-id`, нормализованным `repository` и опциональным `repositoryOwner`, а также `uploadBucket` и `uploadObjectKey` из ответа storage-service. Возвращённый `workflowId` прокидывается клиенту (если его нет — используется `documentId`).
5. Мутация отвечает GraphQL-пейлоадом с сущностью `document`, полями `uploadUrl` и `uploadMethod = PUT`.
6. Клиент загружает файл по `uploadUrl` (HTTP PUT). После события `s3:ObjectCreated:*` MinIO отправляет webhook на `/internal/storage/uploaded`.
7. Webhook-драйвер gateway обрабатывает все записи из уведомления: декодирует `objectKey`, извлекает `documentId` из имени файла и пробует достать `correlation-id` из `x-amz-meta-correlation-id` в user metadata/response elements. Если `correlation-id` отсутствует, генерируется новый. Для каждой записи выполняется gRPC-вызов `document-service.ConfirmUpload` с bucket/objectKey/`correlation-id`. Если ни одна запись не обработана, возвращается 400, иначе 202 Accepted.
8. При необходимости клиент вызывает `retryProcessing(documentId)` — gateway проксирует вызов в `document-service.RetryProcessing` c текущим `correlation-id`.

## Получение статуса
1. Запросы `knowledgeDocument` и `knowledgeDocuments` проксируются в `document-service` с пробросом `x-correlation-id`/`x-api-key` в gRPC metadata.
2. Поддерживаются фильтры по статусу и репозиторию, а также пагинация (`limit`/`offset`).
3. Логи, метрики и трейсинг уходят в OTLP collector; заголовок `X-Correlation-ID` возвращается клиенту.
4. Ответы мапятся в GraphQL-тип `KnowledgeDocument` (статус, workflowId/id, ошибка) и возвращаются клиенту.
