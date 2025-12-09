# Инфраструктурная схема

```mermaid
sequenceDiagram
    participant Client as gRPC клиент
    participant GRPC as DocumentService gRPC
    participant Service as DocumentService
    participant Temporal as Temporal Server
    participant Worker as Temporal Worker
    participant Storage as storage-service (gRPC)
    participant Transformer as transformer-service (gRPC)
    participant Publisher as github-publisher-service (gRPC)
    participant GitHub as GitHub API
    participant DB as PostgreSQL
    participant MinIO as MinIO

    Client->>GRPC: Create/List/Get/UpdateStatus
    GRPC->>Service: обрабатывает запросы
    Service-->>DB: миграции и операции с documents/document_versions/document_status_logs
    Service-->>MinIO: генерация префиксов/ключей для объектов
    Service->>Temporal: запуск DocumentProcessingWorkflow (task queue)
    Worker-->>Temporal: поллинг и исполнение workflow
    Worker-->>DB: фиксация статусов/версий документа
    Worker->>Storage: UploadOriginal/GetObject
    Worker->>Transformer: ConvertDocxToMarkdown
    Worker->>Publisher: PublishMarkdown
    Worker->>GitHub: валидация доступа к репозиторию
```

- PostgreSQL хранит таблицы `documents`, `document_versions`, `document_status_logs`.
- MinIO используется только для генерации ключей/папок; загрузка файлов производится внешними сервисами storage-service/transformer-service.
- Temporal Client в сервисе запускает workflow, а Temporal Worker исполняет его и вызывает внешние gRPC‑сервисы.
- PublishMarkdown взаимодействует с GitHub Publisher Service, перед этим worker напрямую валидирует доступ к GitHub API.
- Корреляционный идентификатор передаётся через метаданные gRPC `x-correlation-id`, прокидывается в workflow и пишется в логах сервера.
