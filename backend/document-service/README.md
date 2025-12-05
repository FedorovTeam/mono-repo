# Document Service

Сервис управляет метаданными документов, версиями и статусами на пути загрузки DOCX → Markdown → GitHub. Хранит ссылки на объекты MinIO и предоставляет gRPC API для создания, чтения, перечисления и обновления статуса документа.

## Архитектура
- PostgreSQL — хранение метаданных, версий и логов статусов.
- MinIO — хранение оригинала и конвертированного Markdown (только ключи на стороне сервиса).
- gRPC — внутренний API `proto/backend/document/document.proto`.

## Конфигурация
Все обязательные параметры находятся в `config.yaml` (путь передается через `CONFIG_PATH`). Значения по умолчанию настроены под инфраструктурные домены и DNS-шаблон `<context>.backend.svc.cluster.local`:
- `server.addr` — адрес gRPC сервера (по умолчанию `:50051`).
- `postgres.dsn` — строка подключения к PostgreSQL.
- `minio.endpoint` — `api.minio.tech.fedorov-team.tech`, `minio.access_key` / `minio.secret_key` — креденшелы, `minio.use_ssl=true`, `minio.original_bucket` / `minio.markdown_bucket` / `minio.bucket_prefix` — обязательные имена бакетов/префикса.
- `temporal.address` — `temporal-frontend.temporal.svc.cluster.local:7233`, `temporal.namespace` — `knowledge-base`, `temporal.task_queue` — имя task queue.
- `services.storage_service` — `storage.backend.svc.cluster.local:50052`, `services.transformer_service` — `transformer.backend.svc.cluster.local:9092`, `services.github_publisher_service` — `github-publisher.backend.svc.cluster.local:9097`.
- `github.repository` — целевой репозиторий (по умолчанию `FedorovTeam/knowledge-base`), `github.branch` — рабочая ветка.

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
