# Document Service

Сервис управляет метаданными документов, версиями и статусами на пути загрузки DOCX → Markdown → GitHub. Хранит ссылки на объекты MinIO и предоставляет gRPC API для создания, чтения, перечисления и обновления статуса документа.

## Архитектура
- PostgreSQL — хранение метаданных, версий и логов статусов.
- MinIO — хранение оригинала и конвертированного Markdown (только ключи на стороне сервиса).
- gRPC — внутренний API `proto/backend/document/document.proto`.

## Быстрый старт
1. Запустите PostgreSQL и MinIO (см. `infrastructure.md`).
2. Выполните миграции: `go run ./cmd/server --migrate`.
3. Сгенерируйте gRPC код: `./proto/generate.sh`.
4. Соберите и запустите сервер: `go run ./cmd/server`.
5. Пример запросов через `grpcurl` описан в `test.md`.

## Структура каталога
- `cmd/server` — точка входа gRPC сервера.
- `internal` — бизнес-логика, репозитории, интеграции.
- `proto/backend/document` — gRPC контракт и сгенерированный код.
- `db/migrations` — SQL миграции для PostgreSQL.
- Документация: `diagrams.md`, `flows.md`, `contracts.md`, `usecases.md`, `tasks.md`, `test.md`, `code.md`, `infrastructure.md`, `sequence.md`.
