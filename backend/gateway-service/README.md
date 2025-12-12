# backend-gateway-service

Публичный GraphQL-шлюз для сценариев загрузки и отслеживания статуса знаниевых документов. Сервис выдаёт подписанные ссылки для прямой загрузки DOCX в MinIO через `storage-service`, проверяет `X-API-Key`, проксирует обращения к `backend-document-service` по gRPC и снабжает запросы трассировками и корреляционными идентификаторами.

## Назначение и ответственность
- Приём GraphQL-мутаций `uploadKnowledgeDocument` (выдача signed URL) и `retryProcessing`, а также запросов `knowledgeDocument` и `knowledgeDocuments` **с обязательным заголовком `X-API-Key` для всех операций (включая `/playground`)**.


- Подключение метрик и трейсинга (OTLP exporter), отдача `X-Correlation-ID` в ответах.
- Проверка API-ключа, заданного в конфигурации (GraphQL и playground требуют `X-API-Key`).
- Приём webhook уведомлений MinIO (`/internal/storage/uploaded`) и передача `ConfirmUpload` в document-service.

## Внешние зависимости
- **backend-document-service** — доменный gRPC API, принимает `CreateDocument`/`GetDocument`/`ListDocuments`/`ConfirmUpload`.
- **storage-service** — выдача signed URL для загрузки оригиналов.
- **MinIO** — целевое хранилище для подписанных ссылок (через `storage-service`).
- **OTLP endpoint/collector** — приём трассировок и метрик от gateway при включённой телеметрии.

gRPC-вызов `storage-service.GenerateUploadURL` ожидает поля `document_id`, `file_name`, `content_length`, `correlation_id`, `expires_in_seconds` и возвращает bucket/object_key/TTL + presigned PUT URL. Корреляционный идентификатор пробрасывается в заголовок `x-amz-meta-correlation-id`, чтобы MinIO сохранил его в метаданных объекта.

## GraphQL API
### Схема (фрагмент)
- `mutation uploadKnowledgeDocument(input: UploadKnowledgeDocumentInput!): UploadKnowledgeDocumentPayload!` — выдаёт `uploadUrl`/`uploadMethod` и создаёт запись в document-service.
- `query knowledgeDocument(id: ID!): KnowledgeDocument` — возвращает статус по идентификатору.
- `query knowledgeDocuments(filter: KnowledgeDocumentFilter, pagination: Pagination): KnowledgeDocumentConnection!` — список документов с фильтрами.

Все GraphQL запросы и мутации требуют заголовок `X-API-Key`; `X-Correlation-ID` (необязательный) прокидывается в gRPC-метаданные `document-service`/`storage-service` и возвращается в ответе.

### Валидация
- Gateway проверяет только пустые строки (`fileName`, `repository`, `branch`, `path`) и то, что `fileSizeBytes` положителен и не превышает `uploads.maxSizeBytes`.
- Проверка формата репозитория (`owner/repo`), нормализация владельца, обязательность ключей/бакетов и прочая бизнес-валидация выполняются в document-service. Ошибки gRPC из document-service возвращаются клиенту без повторной интерпретации.

### Примеры запросов
```bash
API_KEY=secret
ENDPOINT=http://localhost:8080/query

curl -X POST "$ENDPOINT" \
  -H "Content-Type: application/json" \
  -H "X-API-Key: $API_KEY" \
  -H "X-Correlation-ID: demo-corr" \
  -d '{
    "query": "mutation($input: UploadKnowledgeDocumentInput!){ uploadKnowledgeDocument(input:$input){ uploadUrl uploadMethod document{ id status jobId } } }",
    "variables": {"input": {"fileName":"demo.docx","fileSizeBytes":1024,"repository":"org/repo","branch":"main","path":"docs/demo.md"}}
  }'

curl -X POST "$ENDPOINT" \
  -H "Content-Type: application/json" \
  -H "X-API-Key: $API_KEY" \
  -d '{"query": "{ knowledgeDocuments(pagination:{limit:5,offset:0}){ nodes { id status fileName } totalCount } }"}'
```

## Конфигурация
Файл `config/config.yaml` обязателен к заполнению (читается по `CONFIG_PATH`). Ключевые поля по умолчанию используют DNS-имена в формате `<context>.backend.svc.cluster.local`:
- `server.port` — порт HTTP GraphQL сервера.
- `security.apiKey` — ожидаемое значение заголовка `X-API-Key`.
- `documentService.address` — `document.backend.svc.cluster.local:50051`.
- `storageService.address` — `storage.backend.svc.cluster.local:50052`.
- `documentService.grpc` / `storageService.grpc` — настройки gRPC-подключений (TLS через `insecure`, таймауты подключения `dialTimeout`/`minConnectTimeout`, keepalive `time`/`timeout`/`permitWithoutStream`, бэкофф `baseDelay`/`multiplier`/`jitter`/`maxDelay`).
- `uploads.maxSizeBytes`, `uploads.allowedExtensions`, `uploads.signedUrlTtlSeconds` — лимиты загрузки и TTL presigned ссылок.
- `observability.enabled` — включает/отключает экспорт метрик и трассировок (при `false` вместо экспортеров используются no-op провайдеры и обработчики).
- `observability.otlpEndpoint`/`observability.serviceName` — адрес OTLP-коллектора и сервисное имя (обязателен при включённой телеметрии).
- `logging.level` — уровень логирования.
- CORS доступ разрешён только для `https://console.fedorov-team.tech` и `http://localhost:3000` (значения захардкожены в коде).

## Локальный запуск
1. Сгенерировать GraphQL код: `go run github.com/99designs/gqlgen generate`.
2. Запустить сервер: `go run ./cmd/server` (предварительно настроить `config/config.yaml` и OTLP endpoint).
3. Проверить health GraphQL-пингом: `curl -H "X-API-Key: secret" -d '{"query":"{ __typename }"}' http://localhost:8080/query`.

## Структура
- `cmd/server` — точка входа.
- `graph/` — схема и gqlgen-генерация.
- `internal/clients` — адаптеры gRPC клиентов document-service и storage-service.
- `internal/server` — HTTP/GraphQL сервер и middleware.
- `internal/config` — загрузка конфигурации.

