# backend-gateway-service

Публичный GraphQL-шлюз для сценариев загрузки и повторной обработки знаниевых документов. Сервис принимает запросы от внешних клиентов, проверяет `X-API-Key`, проксирует обращения к `backend-document-service` по gRPC и запускает Temporal workflow цепочки upload→convert→publish.

## Назначение и ответственность
- Приём внешних GraphQL-мутаций `uploadKnowledgeDocument` и `retryProcessing`.
- Чтение статусов через запросы `knowledgeDocument` и `knowledgeDocuments`.
- Прокладка `correlation-id` в логи и метаданные gRPC/Temporal.
- Проверка API-ключа, заданного в конфигурации.

## Внешние зависимости
- **Temporal** — запуск workflow загрузки/конвертации/публикации.
- **PostgreSQL** — хранение статусов в `backend-document-service`.
- **MinIO** — хранение исходных и конвертированных файлов (через document-service).
- **backend-document-service** — доменный gRPC API.

## Конфигурация
Файл `config/config.yaml` содержит:
- `server.port` — порт HTTP GraphQL сервера.
- `security.apiKey` — ожидаемое значение заголовка `X-API-Key`.
- `documentService.address` — адрес gRPC документ-сервиса.
- `temporal` — namespace и task queue для workflow.
- `logging.level` — уровень логирования.

## Локальный запуск
1. Сгенерировать GraphQL код: `go run github.com/99designs/gqlgen generate`.
2. Запустить сервер: `go run ./cmd/server` (предварительно настроить `config/config.yaml`).
3. Проверить health GraphQL-пингом: `curl -H "X-API-Key: secret" -d '{"query":"{ __typename }"}' http://localhost:8080/query`.

## Структура
- `cmd/server` — точка входа.
- `graph/` — схема и gqlgen-генерация.
- `internal/clients` — адаптеры gRPC и Temporal.
- `internal/server` — HTTP/GraphQL сервер и middleware.
- `internal/config` — загрузка конфигурации.

