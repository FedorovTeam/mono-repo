# Code / Module View

```mermaid
flowchart TD
    main("cmd/main.go") --> configLoader{{"internal/config"}}
    main --> observability{{"internal/observability"}}
    main --> migrations{{"db/migrations"}}
    main --> repo
    main --> keyManager
    main --> temporalClient["Temporal client"]
    main --> grpcServer

    migrations --> db[(PostgreSQL)]
    repo[(internal/repository)] --> db
    keyManager[(internal/storage.MinioKeyManager)] --> minio[(MinIO SDK)]

    grpcServer[(internal/transport/grpc)] --> service
    service[(internal/service/document.Service)] --> repo
    service --> keyManager
    service --> temporalClient

    temporalClient --> worker["Temporal worker"]
    worker --> activities[(Activities)]
    activities --> storageClient["storage-service gRPC client"]
    activities --> transformerClient["docling-transformer-service gRPC client"]
    activities --> publisherClient["github-publisher-service gRPC client"]
```

Основные части сервиса:

- **Точка входа `cmd/main.go`**: загружает конфигурацию, настраивает логирование и OpenTelemetry, прогоняет миграции PostgreSQL, создаёт MinIO‑клиент и `MinioKeyManager`, инициализирует репозиторий, Temporal‑клиент и gRPC‑клиенты зависимых сервисов (`storage`, `transformer`, `github-publisher`). Регистрирует Temporal worker с активити и запускает gRPC‑сервер. 【F:cmd/main.go†L1-L128】
- **`internal/service/document`**: бизнес-логика создания и управления документами, запуск Temporal workflow из `ConfirmUpload`, валидация статусов и генерация ключей через `KeyManager`. Содержит `DocumentProcessingWorkflow` и активити (`AwaitOriginal`, `ConvertToMarkdown`, `PublishMarkdown`, `UploadOriginal`). 【F:internal/service/document/service.go†L1-L172】【F:internal/service/document/workflow.go†L1-L225】
- **Temporal активити**: обращаются к внутреннему репозиторию для обновления статусов, используют gRPC‑клиенты `storage`, `transformer` и `github-publisher`, валидируют наличие объектов и репозитория GitHub, прокидывают `x-correlation-id`. 【F:internal/service/document/workflow.go†L131-L334】
- **`internal/repository`**: CRUD по таблицам `documents`, `document_versions`, `document_status_logs` с контролем переходов статусов и версионирования объектных ключей. 【F:internal/repository/repository.go†L1-L241】
- **`internal/storage`**: `MinioKeyManager` проверяет существование бакетов и строит пути объектных ключей на основе префикса и метаданных документа. 【F:internal/storage/minio.go†L1-L86】
- **`internal/transport/grpc`**: серверная обёртка над proto `DocumentService`, маппинг моделей и логирование `x-correlation-id` для входящих вызовов. 【F:internal/transport/grpc/server.go†L1-L128】
- **Конфигурация и наблюдаемость**: пакеты `internal/config` и `internal/observability` используются на старте для загрузки `config.yaml` и настройки логов/метрик/traces. 【F:cmd/main.go†L1-L73】
