# Code / Module view

```mermaid
graph TD
    main[cmd/main] --> cfg[internal/config]
    main --> telemetry[internal/infrastructure/telemetry]
    main --> migrations[db/migrations]
    main --> storage[internal/infrastructure/storage]
    main --> repoImpl[internal/infrastructure/repository]
    main --> converter[internal/infrastructure/converter]
    main --> usecases[internal/usecase]
    main --> grpc[internal/interfaces/grpc]
    main --> domain[internal/domain]
    usecases --> repoIface[internal/repository]
    repoIface --> repoImpl
    repoImpl --> postgres[(Postgres)]
    usecases --> storage
    storage --> minio[(MinIO)]
    usecases --> converter
    converter --> doclingjson[internal/infrastructure/doclingjson]
    converter --> telemetry
    usecases --> backoff[internal/backoff]
    usecases --> domain
    grpc --> usecases
    grpc --> proto[proto/backend/transformer/v1]
    telemetry --> cfg
```

- `cmd/main` загружает конфигурацию (включая блок `docling_render`), инициализирует логгер и OTEL‑провайдеры, прогоняет миграции, создаёт клиентов MinIO и Postgres‑пула, восстанавливает подвисшие задачи, собирает Docling‑конвертер, поднимает воркер-процессор очереди и gRPC сервер.
- `internal/config` читает YAML‑конфигурацию сервиса с блоками Service, Scheduler, Docling (включая storage и статусный backoff), DoclingRender, MinIO, Postgres и Observability, применяет значения по умолчанию и валидирует параметры.
- `internal/interfaces/grpc` поднимает gRPC сервер `TransformerService` из proto, валидирует вход и correlation-id, проксирует вызовы юзкейса `TransformerUseCase` (очередь `ConvertDocumentToMarkdown` с optional `wait_for_result` и синхронный `ConvertDocxToMarkdown`).
- `internal/usecase` реализует доменный слой: `TransformerService` ставит задачи в очередь, переиспользует актуальные задания, рассчитывает ключи/бакеты/расширения и backoff статусов; `Processor`/`ConversionExecutor`/`DoclingPoller` управляют обработкой очереди, поллят Docling, загружают готовый Markdown в хранилище, пишут метрики и ведут ретраи/реанимацию.
- `internal/repository` описывает интерфейс `QueueRepository` для работы с очередью Docling‑трансформаций и операциями по восстановлению/очистке.
- `internal/infrastructure/repository` реализует `QueueRepository` для Postgres (`transformations_queue`): постановка и выбор задач, обновление статусов Docling, восстановление зависших, requeue/poll расписание и очистку завершённых.
- `internal/infrastructure/storage` оборачивает MinIO‑клиент для скачивания исходного файла и загрузки результата конвертации в нужный бакет/ключ.
- `internal/infrastructure/converter` реализует HTTP‑клиент Docling: submit задач (upload_file/source_async) с ретраями и заголовками correlation/API key, поллинг статуса/результата, выбор медиа-типов, построение multipart, поддержка MinIO‑стоража Docling и опциональный рендер Markdown из Docling JSON/таблиц.
- `internal/infrastructure/doclingjson` парсит JSON Docling и собирает Markdown по таблицам/marks.
- `internal/domain` содержит статусы, сущности очереди и утилиты: определение контент‑тайпов, нормализация расширений и построение ключей для Markdown.
- `internal/infrastructure/telemetry` предоставляет генерацию correlation-id, настройку OTEL провайдеров и вспомогательные опции для gRPC telemetry handler и логгера.
- `db/migrations` содержит миграции Postgres для таблицы `transformations_queue` и связанных индексов/колонок.
