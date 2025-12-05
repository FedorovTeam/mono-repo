# Инфраструктурная схема

```mermaid
flowchart LR
    client[gRPC client] --> |Create/List/Get/UpdateStatus| grpcServer
    grpcServer --> service[DocumentService]
    service --> db[(PostgreSQL)]
    service --> minio[MinIO]
```

- PostgreSQL хранит таблицы `documents`, `document_versions`, `document_status_logs`.
- MinIO используется только для генерации ключей/папок; загрузка файлов производится внешними сервисами.
- Корреляционный идентификатор передаётся через метаданные gRPC `x-correlation-id` и пишется в логах сервера.
