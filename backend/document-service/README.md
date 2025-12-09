# Document Service

Сервис управляет метаданными документов, версиями и статусами на пути загрузки DOCX → Markdown → GitHub. Хранит ссылки на объекты MinIO и предоставляет gRPC API для создания, чтения, перечисления и обновления статуса документа.

## Архитектура
- PostgreSQL — хранение метаданных, версий и логов статусов.
- MinIO — хранение оригинала и конвертированного Markdown (только ключи на стороне сервиса).
- gRPC — внутренний API `proto/backend/document/document.proto`.

## Статусы и повторная обработка
- Жизненный цикл документа фиксируется в статусах `Uploaded → StoredOriginal → ConvertedToMarkdown → PublishedToGitHub` с отдельным состоянием `Failed`.
- Workflow запускается по событию `ConfirmUpload` (после загрузки исходника в MinIO) и проходит шаги `await original → convert → publish`, обновляя статус в PostgreSQL после каждого вызова внешних сервисов.
- Встроенная retry-политика Temporal повторяет активности при временных ошибках; при постоянной ошибке статус меняется на `Failed` с причиной из активности.
- Для повторной обработки достаточно заново вызвать `ConfirmUpload` с тем же `document_id` и новым объектом в MinIO — новый Workflow стартует с тем же идентификатором и перезапишет статусы.

## Формат ключей в MinIO
Сервис формирует ключи по шаблону `<prefix>/year=YYYY/month=MM/day=DD/<document_id>/<uuid>[.<ext>]/{original|markdown}`. Если `document_id` отсутствует, используется только сгенерированный UUID. Расширение сохраняется из исходного имени файла (если оно передано), но сам путь публикации или имя не влияют на итоговый ключ. Пример итогового ключа:

```
documents/year=2024/month=05/day=09/6c0c24a8-4c83-4e5b-8ab5-21f0db363e51/9b902e58-6c9e-4f05-b7e6-2a0d6617d063.md/original
```

Группировка по дате (`year=`/`month=`/`day=`) позволяет на уровне lifecycle policy назначать разные правила хранения для старых объектов, а доменный путь внутри сохраняет стабильную привязку к `document_id` при случайном имени файла.

## Конфигурация
Все обязательные параметры находятся в `config.yaml` (путь передается через `CONFIG_PATH`). Значения по умолчанию настроены под инфраструктурные домены и DNS-шаблон `<context>.backend.svc.cluster.local`:
- `server.addr` — адрес gRPC сервера (по умолчанию `:50051`).
- `postgres.dsn` — строка подключения к PostgreSQL.
- `minio.endpoint` — `api.minio.tech.fedorov-team.tech`, `minio.access_key` / `minio.secret_key` — креденшелы, `minio.use_ssl=true`, `minio.original_bucket` / `minio.markdown_bucket` / `minio.bucket_prefix` — обязательные имена бакетов/префикса.
- `temporal.address` — `temporal-frontend.temporal.svc.cluster.local:7233`, `temporal.namespace` — `knowledge-base`, `temporal.task_queue` — имя task queue.
- `services.storage_service` — `storage.backend.svc.cluster.local:50052`, `services.transformer_service` — `transformer.backend.svc.cluster.local:9092`, `services.github_publisher_service` — `github-publisher.backend.svc.cluster.local:9097`.
- `services.grpc` — общие параметры подключения к gRPC клиентам: `dial_timeout` для `grpc.WithBlock`, `min_connect_timeout` и `backoff` для `grpc.WithConnectParams`, `keepalive` для `grpc.WithKeepaliveParams` (все значения задаются в формате `time.Duration`).
- `github.repository` — целевой репозиторий (по умолчанию `FedorovTeam/knowledge-base`), `github.branch` — рабочая ветка. На входе сервис ожидает раздельные `owner` и `repository`; если в `repository` передан формат `owner/repo`, владелец выделяется автоматически, а в базе сохраняется только имя репозитория без префикса владельца.
- `observability.logs.enabled` — включает вывод стандартных логов; при `false` логгер работает в no-op режиме.
- `observability.traces.enabled` / `observability.metrics.enabled` — включает экспорт OTLP-трейсов и метрик через gRPC. Для работы требуется доступный OTLP Collector (адрес берется из `OTEL_EXPORTER_OTLP_ENDPOINT`, по умолчанию `localhost:4317` с нешифрованным соединением).

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
