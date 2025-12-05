# Code / Module View

```mermaid
flowchart LR
    main(cmd/server) --> grpcServer
    grpcServer --> documentService
    documentService --> repo[(DocumentRepository)]
    documentService --> minioKeyManager
    repo --> db[(PostgreSQL)]
    minioKeyManager --> minio[(MinIO SDK)]
```

Основные пакеты:
- `internal/service/document` — бизнес-логика, валидация статусов.
- `internal/repository` — работа с PostgreSQL через `database/sql`.
- `internal/storage` — генерация ключей MinIO и проверки бакетов.
- `internal/transport/grpc` — серверная обёртка над gRPC методами.
