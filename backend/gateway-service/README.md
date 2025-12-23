# backend-gateway-service

Публичный GraphQL-шлюз для загрузки знаниевых документов: выдаёт presigned URL через `storage-service`, создаёт записи в `document-service`, принимает MinIO webhook для подтверждения загрузки и прокидывает `Authorization`/`X-API-Key` (если включён)/`X-Correlation-ID` и данные пользователя (`x-user-id`, `x-user-email`, `x-user-groups`) в downstream gRPC вызовы.

## Назначение и ответственность
- Обработка GraphQL запросов `knowledgeDocument`/`knowledgeDocuments` и мутации `uploadKnowledgeDocument` на `/query` (Playground — `/playground`) с обязательным заголовком `Authorization: Bearer <jwt>` по OIDC (`issuer https://auth.tech.fedorov-team.tech/application/o/console/`, `audience L6AmiswYzea2gAs5aGfzhLP9jlapG42ppPhtutoF`); если `security.apiKey.enabled=true`, допускается альтернатива `X-API-Key`. `CorrelationIDMiddleware` генерирует/пробрасывает `X-Correlation-ID` и возвращает его в ответе. CORS разрешён только для `https://console.fedorov-team.tech` и `http://localhost:3000`, заголовок `Authorization` разрешён.
- Выдача signed URL: `uploadKnowledgeDocument` валидирует, что `fileName`/`repository`/`branch` непустые и `fileSizeBytes > 0`, генерирует `documentId` (UUID), триммит `path` и при пустом значении подставляет `knowledge/docs/<documentId>.md`, вызывает `storage-service.GenerateUploadURL` с TTL из конфигурации и передаёт ответные bucket/object ключи в `document-service.CreateDocument`.
- Проксирование gRPC вызовов: `Authorization` (данные пользователя) и `X-API-Key` (если включён), а также `x-correlation-id` из контекста передаются в metadata клиентов document/storage-service; для webhook ConfirmUpload используется только correlation-id.
- Приём вебхука `/internal/storage/uploaded` от MinIO: парсинг `s3:ObjectCreated:*`, `PathUnescape` objectKey, извлечение `documentId` из basename и корреляции из `x-amz-meta-correlation-id` (userMetadata/responseElements, case-insensitive) с генерацией нового идентификатора при отсутствии; подтверждение загрузки через `document-service.ConfirmUpload` и ответ `202 Accepted`, либо `400 Bad Request`, если нет обработанных записей или payload невалиден.
- Наблюдаемость: HTTP- и gRPC-клиенты обёрнуты OTLP-инструментацией; при выключенной телеметрии используются no-op провайдеры.

## Внешние зависимости
- **document-service** — gRPC API: `CreateDocument`, `GetDocument`, `ListDocuments`, `ConfirmUpload`.
- **storage-service** — gRPC API: `GenerateUploadURL` (ожидает `document_id`, `file_name`, `content_length`, `correlation_id`, `expires_in_seconds`; возвращает PUT `uploadUrl`, `bucket/objectKey`, `markdownBucket/markdownObjectKey`).
- **MinIO** — источник уведомлений `s3:ObjectCreated:*` (через webhook).
- **OTLP collector** — при `observability.enabled=true` для отправки трейсов и метрик по gRPC.

## GraphQL API
- `mutation uploadKnowledgeDocument(input: UploadKnowledgeDocumentInput!): UploadKnowledgeDocumentPayload!` — возвращает `uploadUrl`, `uploadMethod="PUT"` и созданный документ (статус из document-service, `repositoryOwner` — если пришёл из gRPC, `jobId` выбирается по приоритету `job_id` → `workflow_id` → `id`, `error` при наличии). Gateway не разбирает владельца репозитория: строка `repository` передается как есть.
- `query knowledgeDocument(id: ID!): KnowledgeDocument` — возвращает документ по идентификатору.
- `query knowledgeDocuments(filter, pagination): KnowledgeDocumentConnection!` — постраничный список (`limit`/`offset`, по умолчанию `10/0`) с фильтрами по статусу и `repository`.
- Статусы: `UPLOADED`, `STORED_ORIGINAL`, `CONVERTED_TO_MARKDOWN`, `PUBLISHED_TO_GITHUB`, `FAILED`.

### Пример
```bash
API_KEY=secret # используется, только если включен security.apiKey.enabled
ENDPOINT=http://localhost:8080/query

curl -X POST "$ENDPOINT" \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer <token>" \
  -H "X-Correlation-ID: demo-corr" \
  -d '{
    "query": "mutation($input: UploadKnowledgeDocumentInput!){ uploadKnowledgeDocument(input:$input){ uploadUrl uploadMethod document{ id status jobId path repositoryOwner } } }",
    "variables": {"input": {"fileName":"demo.docx","fileSizeBytes":1024,"repository":"org/repo","branch":"main","path":"docs/demo.md"}}
  }'

curl -X POST "$ENDPOINT" \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer <token>" \
  -d '{"query": "{ knowledgeDocuments(pagination:{limit:5,offset:0}){ nodes { id status fileName repository } totalCount } }"}'
```

## Конфигурация
Файл `config/config.yaml` обязателен; путь передаётся флагом `-config` (по умолчанию `config/config.yaml`).
- `server.port` — порт HTTP GraphQL сервера.
- `security.apiKey.enabled` — включает аутентификацию по `X-API-Key` (true по умолчанию); `security.apiKey.value` — ожидаемое значение (обязательно при enabled=true).
- `security.oidc.jwksUrl`/`issuer`/`audience` — параметры проверки `Authorization: Bearer` токена; по умолчанию настроены на `https://auth.tech.fedorov-team.tech/application/o/console/`.
- `documentService.address` / `storageService.address` — gRPC адреса (обязательны). Пример по умолчанию: `document-service.document-service.svc.cluster.local:9090`, `storage-service.storage-service.svc.cluster.local:9090`.
- `documentService.grpc` / `storageService.grpc` — настройки gRPC клиентов: `insecure` (по умолчанию `true`), `dialTimeout` (5s), `minConnectTimeout` (3s), keepalive (`time=5m`, `timeout=10s`, `permitWithoutStream=false`), бэкофф (`baseDelay=1s`, `multiplier=1.6`, `jitter=0.2`, `maxDelay=30s`), `WaitForReady` включён.
- `uploads.signedUrlTtlSeconds` — TTL presigned ссылок; если не задано или ≤0, используется `900`.
- `observability.enabled` — по умолчанию `true`; при `true` обязателен `observability.otlpEndpoint`, `observability.serviceName` по умолчанию `backend-gateway-service`.
- `logging.level` — уровень логирования.

## Наблюдаемость
- HTTP обработчики (`/query`, `/playground`, `/internal/storage/uploaded`) оборачиваются `otelhttp` при `observability.enabled=true`.
- gRPC клиенты storage/document-service получают `otelgrpc` stats handler с propagation; при выключенной телеметрии используются no-op провайдеры без отправки данных.

## Локальный запуск
1. Убедиться, что `config/config.yaml` заполнен (минимум `security.oidc` и/или `security.apiKey`, адреса gRPC-сервисов; указать `observability.enabled: false`, если нет OTLP collector).
2. Сгенерировать GraphQL код (если схема менялась): `go run github.com/99designs/gqlgen generate`.
3. Запустить сервер: `go run ./cmd -config config/config.yaml`.
4. Проверить доступность: `curl -H "Authorization: Bearer <token>" -d '{"query":"{ __typename }"}' http://localhost:<port>/query`.

## Структура
- `cmd/` — точка входа сервера.
- `graph/` — схема GraphQL и resolvers.
- `internal/config` — загрузка и валидация конфигурации.
- `internal/infrastructure/clients` — gRPC клиенты document/storage-service.
- `internal/infrastructure/webhook` — парсер уведомлений MinIO.
- `internal/usecase` — сценарии upload/status/webhook.
- `internal/server` — HTTP сервер, маршруты и middleware.
- `pkg/` — вспомогательные пакеты (ctxutils, middleware, grpcdial, observability, logging).
