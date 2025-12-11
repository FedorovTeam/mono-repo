# Code / Module View

```mermaid
flowchart TD
    main("cmd/main.go") --> configLoader{{"internal/config"}}
    main --> observability{{"internal/observability"}}
    main --> migrations{{"db/migrations"}}
    main --> repo
    main --> minioClient["MinIO client"]
    main --> grpcDial{{"internal/grpcdial"}}
    main --> keyManager
    main --> jobWorkers["cron workers (store → convert → publish)"]
    main --> uploadReconciler["UploadReconciler"]
    main --> grpcServer

    migrations --> db[(PostgreSQL: documents, processing_jobs, job_events)]
    repo[(internal/repository)] --> db
    keyManager[(internal/storage.MinioKeyManager)] --> minioClient

    grpcDial --> storageClient["storage-service gRPC client"]
    grpcDial --> transformerClient["docling-transformer-service gRPC client"]
    grpcDial --> publisherClient["github-publisher-service gRPC client"]

    grpcServer[(internal/transport/grpc)] --> service
    service[(internal/service/document.Service)] --> repo
    service --> keyManager
    service --> jobWorkers

    jobWorkers --> repo
    jobWorkers --> storageClient
    jobWorkers --> transformerClient
    jobWorkers --> publisherClient

    uploadReconciler --> repo
    uploadReconciler --> minioClient
    uploadReconciler --> service
```

Основные части сервиса:

- **Точка входа `cmd/main.go`**: загружает конфигурацию, настраивает логирование и OpenTelemetry, прогоняет миграции PostgreSQL, создаёт MinIO‑клиент и `MinioKeyManager`, инициализирует репозиторий и gRPC‑клиенты зависимых сервисов (`storage`, `transformer`, `github-publisher`) через `internal/grpcdial`. Поднимает периодические задачи (`UploadReconciler`, cron‑воркеры по стадиям store/convert/publish) и запускает gRPC‑сервер. 【F:cmd/main.go†L1-L137】【F:cmd/main.go†L143-L204】【F:cmd/main.go†L211-L240】
- **`internal/service/document`**: бизнес-логика создания и управления документами, постановка задач в `processing_jobs`, валидация статусов и генерация ключей через `KeyManager`. Сервис остаётся источником статусов `UPLOADED → STORED_ORIGINAL → CONVERTED_TO_MARKDOWN → PUBLISHED_TO_GITHUB → FAILED` в таблице `documents`. 【F:internal/service/document/service.go†L1-L226】【F:internal/repository/repository.go†L1-L241】
- **Крон-воркеры**: читают очередь `processing_jobs` через `SELECT ... FOR UPDATE SKIP LOCKED`, учитывают расписание `next_run_at`, обновляют статус задач и пишут события в `job_events`. Для конвертации и публикации используют gRPC‑клиенты `storage`, `transformer` и `github-publisher`, прокидывая `x-correlation-id` из `payload`. 【F:internal/service/document/reconciler.go†L14-L103】【F:internal/service/document/retrier.go†L14-L100】
- **`internal/storage`**: `MinioKeyManager` проверяет существование бакетов и строит пути объектных ключей на основе префикса и метаданных документа. 【F:internal/storage/minio.go†L1-L86】
- **`internal/transport/grpc`**: серверная обёртка над proto `DocumentService`, маппинг моделей и логирование `x-correlation-id` для входящих вызовов. 【F:internal/transport/grpc/server.go†L1-L128】
- **Конфигурация и наблюдаемость**: пакеты `internal/config` и `internal/observability` используются на старте для загрузки `config.yaml` и настройки логов/метрик/traces. 【F:cmd/main.go†L1-L83】
