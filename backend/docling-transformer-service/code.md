# Code / Module view

```mermaid
graph TD
    main[cmd/main] --> cfg[internal/config]
    main --> telemetry[internal/infrastructure/telemetry]
    main --> migrations[db/migrations]
    main --> storage[internal/infrastructure/storage]
    main --> repo[internal/infrastructure/repository]
    main --> conv[internal/infrastructure/converter]
    main --> usecases[internal/usecase]
    main --> grpcsrv[internal/interfaces/grpc]
    usecases --> repo
    usecases --> conv
    usecases --> storage
    grpcsrv --> usecases
    grpcsrv --> proto[proto/backend/transformer/v1]
    repo --> postgres[(Postgres)]
    storage --> minio[(MinIO)]
    conv --> telemetry
    telemetry --> cfg
```

- `cmd/main` загружает конфигурацию, инициализирует логгер и OTEL‑провайдеры, прогоняет миграции, поднимает клиентов MinIO и Postgres, собирает конвертер Docling, запускает юзкейсы и gRPC сервер.
- `internal/config` читает YAML‑конфигурацию сервиса с блоками Service, Scheduler, Docling, MinIO, Postgres и Observability.
- `internal/interfaces/grpc` поднимает gRPC сервер и регистрирует `TransformerService`, валидирует вход, подставляет значения по умолчанию для bucket/ключей и делегирует работу юзкейсам.
- `internal/usecase` реализует сценарии постановки и обработки задач конвертации, опираясь на доменные интерфейсы и не завися от транспорта или СУБД.
- `internal/infrastructure/repository` реализует Postgres‑очередь трансформаций: постановка задач, выбор следующей, обновление статусов Docling, восстановление зависших, уборка завершённых и сбор статистики.
- `internal/usecase` управляет воркерами обработки задач: скачивает исходники из MinIO, отправляет их в Docling, поллит статус с экспоненциальной задержкой и ограничением попыток, загружает Markdown обратно в MinIO, пишет метрики и восстанавливает зависшие задачи.
- `internal/infrastructure/storage` оборачивает клиент MinIO для скачивания исходного файла и загрузки результата конвертации в указанный бакет.
- `internal/infrastructure/converter` реализует HTTP‑клиент Docling: отправка файла с заголовками correlation-id, API‑ключом и именем источника, повторные попытки отправки, нормализация статусов и поллинг результата.
- `internal/domain` содержит значения и сущности трансформации (статусы, задача очереди) и утилиты для построения ключей.
- `internal/infrastructure/telemetry` предоставляет генерацию correlation-id, настройку OTEL провайдеров и вспомогательные опции для gRPC telemetry handler и логгера.
- `db/migrations` содержит миграции Postgres для таблицы очереди трансформаций и выполняется на старте сервиса.
