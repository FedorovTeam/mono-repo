# backend-gateway-service

Публичный GraphQL-шлюз для сценариев загрузки и повторной обработки знаниевых документов. Сервис выдаёт подписанные ссылки для прямой загрузки DOCX в MinIO через `storage-service`, проверяет `X-API-Key`, проксирует обращения к `backend-document-service` по gRPC и запускает Temporal workflow цепочки upload→convert→publish с трассировками OpenTelemetry.

## Назначение и ответственность
- Приём внешних GraphQL-мутаций `uploadKnowledgeDocument` (выдача signed URL) и `retryProcessing`.
- Чтение статусов через запросы `knowledgeDocument` и `knowledgeDocuments`.
- Валидация размера/расширения и прокладка `correlation-id` в логи и метаданные gRPC/Temporal/MinIO.
- Подключение метрик и трейсинга (OTLP exporter), отдача `X-Correlation-ID` в ответах.
- Проверка API-ключа, заданного в конфигурации.

## Внешние зависимости
- **Temporal** — запуск workflow загрузки/конвертации/публикации.
- **PostgreSQL** — хранение статусов в `backend-document-service`.
- **MinIO** — хранение исходных и конвертированных файлов (через `storage-service`).
- **backend-document-service** — доменный gRPC API.
- **storage-service** — выдача signed URL для загрузки оригиналов.
- **OTLP endpoint/collector** — приём трассировок и метрик от gateway.

## Конфигурация
Файл `config/config.yaml` содержит:
- `server.port` — порт HTTP GraphQL сервера.
- `security.apiKey` — ожидаемое значение заголовка `X-API-Key`.
- `documentService.address` — адрес gRPC документ-сервиса.
- `storageService.address` — адрес gRPC `storage-service` для выдачи signed URL.
- `uploads.maxSizeBytes`, `uploads.allowedExtensions`, `uploads.signedUrlTtlSeconds` — серверная валидация и TTL для presigned ссылок.
- `temporal` — namespace и task queue для workflow.
- `observability.otlpEndpoint`/`observability.serviceName` — конфигурация OTLP-экспортера метрик и трейсинга.
- `logging.level` — уровень логирования.

## Локальный запуск
1. Сгенерировать GraphQL код: `go run github.com/99designs/gqlgen generate`.
2. Запустить сервер: `go run ./cmd/server` (предварительно настроить `config/config.yaml` и OTLP endpoint).
3. Проверить health GraphQL-пингом: `curl -H "X-API-Key: secret" -d '{"query":"{ __typename }"}' http://localhost:8080/query`.

## Структура
- `cmd/server` — точка входа.
- `graph/` — схема и gqlgen-генерация.
- `internal/clients` — адаптеры gRPC и Temporal.
- `internal/server` — HTTP/GraphQL сервер и middleware.
- `internal/config` — загрузка конфигурации.

