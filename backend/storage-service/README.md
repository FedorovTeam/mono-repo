# storage-service

Сервис принимает и отдаёт объекты в MinIO, а также фиксирует факт сохранения оригинала через очередь в PostgreSQL. Публичный контракт описан в `proto/backend/storage/v1/storage.proto` и реализует gRPC-методы `UploadOriginal`, `UploadMarkdown`, `GetObject` и `GenerateUploadURL`.

## Основные возможности
- Генерация ключей объектов по шаблону `org/<org_slug>/project/<project_slug>/year=YYYY/month=MM/day=DD/<uuid>/<document_id>.<ext>` на основе текущей даты и случайного UUID. Для presigned ссылок используются сервисные `org_slug=knowledge` и `project_slug=documents`; для загрузок по gRPC slug’и берутся из запроса, затем из метаданных `org-slug`/`project-slug`, иначе применяются те же дефолты. 【F:internal/storage/key_generator.go†L26-L68】【F:internal/transport/grpcserver/server.go†L37-L115】
- Загрузка оригиналов и Markdown: валидация обязательных полей, проверка доступности бакета, сохранение с Content-Type по расширению (markdown — `text/markdown`). В ответ добавляются заголовки `x-correlation-id`, `org-slug`, `project-slug` и `x-amz-meta-correlation-id` при наличии корреляции из поля запроса или `x-correlation-id` в метаданных. 【F:internal/transport/grpcserver/server.go†L55-L115】
- Получение объекта по `bucket`/`object_key` с проверкой существования бакета и добавлением служебных заголовков; данные читаются напрямую из MinIO. 【F:internal/transport/grpcserver/server.go†L117-L156】【F:internal/storage/minio_service.go†L107-L136】
- Выдача presigned PUT (`GenerateUploadURL`): обязательные `document_id`, `file_name`, `content_length>0`; расширение должно входить в `uploads.allowed_extensions`, размер — не превышать `uploads.max_size_bytes`. TTL берётся из `expires_in_seconds` или `uploads.signed_url_ttl_seconds`. В ссылку добавляется `x-amz-meta-correlation-id` из запроса/метаданных; возвращаются пары ключей для исходника и Markdown. 【F:internal/storage/minio_service.go†L138-L211】【F:internal/transport/grpcserver/server.go†L158-L201】
- При старте создаются (если нет) бакеты `minio.buckets.knowledge_docx`, `minio.buckets.knowledge_md` и `events.upload_bucket` (если отличается), после чего настраивается webhook/очередь MinIO для событий `ObjectCreated` с ARN из `events.webhook_arn`. Отсутствие bucket’а или ARN приводит к ошибке запуска. 【F:cmd/main.go†L26-L67】【F:internal/storage/minio_service.go†L213-L262】【F:internal/storage/notification_setup.go†L8-L34】
- Фоновый воркер в том же процессе опрашивает таблицу `processing_jobs` для стадии `processing.stage` (дефолт `store_original`) и переводит документ в статус `StoredOriginal`, логируя события в `job_events` и `document_status_logs`. Повторные попытки выполняются с отложенным `next_run_at = now() + attempts * backoff` до `processing.max_attempts`, после чего задача помечается `failed`. 【F:cmd/worker.go†L10-L39】【F:internal/application/worker_service.go†L45-L113】【F:internal/infrastructure/persistence/processing_job_repository.go†L18-L93】

## Бакеты и ключи
- Настраиваемые бакеты: `minio.buckets.knowledge_docx` (оригиналы/загрузка по ссылке) и `minio.buckets.knowledge_md` (Markdown). Для presigned URL используется `events.upload_bucket`, иначе `minio.buckets.knowledge_docx`. Все бакеты проверяются на доступность перед операциями. 【F:cmd/main.go†L30-L53】【F:internal/storage/minio_service.go†L107-L167】
- Ключи формируются только при непустых `org_slug`, `project_slug`, `document_id` и расширении; иначе возвращается ошибка `INVALID_ARGUMENT`. 【F:internal/storage/key_generator.go†L35-L68】【F:internal/storage/minio_service.go†L138-L187】

## Очередь и статусы
- Рабочая таблица: `processing_jobs` со статусами `pending` → `in_progress` → `completed`/`failed`; отбор ведётся по `stage` и `next_run_at<=now()` с `FOR UPDATE SKIP LOCKED`. История фиксируется в `job_events` (event_type `processing`/`retry`/`completed`). 【F:internal/infrastructure/persistence/processing_job_repository.go†L20-L87】
- При обработке запись документа обновляется до `StoredOriginal` (поля `status`, `error_reason`) с логом в `document_status_logs`, где `workflow_id` берётся из `payload["correlation_id"]`. Ошибки сохраняются в `payload.last_error` при пересчёте. 【F:internal/infrastructure/persistence/document_repository.go†L16-L34】【F:internal/application/worker_service.go†L82-L113】

## Конфигурация
Конфиг читается из `CONFIG_PATH` (по умолчанию `config/config.yaml`); дефолты подставляются для observability и параметров воркера/загрузок.

- `service.grpc_port` — адрес gRPC-сервера. Поле `service.document_service` присутствует в конфигурации, но текущая реализация его не использует. 【F:config/config.go†L14-L76】【F:cmd/main.go†L74-L103】
- `minio.endpoint`, `minio.access_key`, `minio.secret_key`, `minio.use_ssl`, `minio.buckets.knowledge_docx`, `minio.buckets.knowledge_md` — доступы и приватные бакеты. 【F:config/config.go†L39-L63】【F:cmd/main.go†L26-L53】
- `events.upload_bucket`, `events.webhook_arn` — бакет для presigned загрузок и обязательный ARN получателя уведомлений. 【F:config/config.go†L24-L38】【F:internal/storage/notification_setup.go†L8-L34】
- `uploads.max_size_bytes`, `uploads.allowed_extensions` (по умолчанию `doc`, `docx`, `pdf`), `uploads.signed_url_ttl_seconds` (по умолчанию 900) — ограничения при выдаче presigned URL. 【F:config/config.go†L64-L106】【F:internal/storage/minio_service.go†L148-L187】
- `processing.dsn` (обязателен), `processing.stage` (дефолт `store_original`), `processing.poll_interval_seconds` (дефолт 5), `processing.max_attempts` (дефолт 3), `processing.backoff_seconds` (дефолт 5) — настройки фонового воркера. 【F:config/config.go†L77-L135】【F:cmd/worker.go†L10-L39】
- `observability.enable_metrics`, `observability.enable_traces`, `observability.enable_logs` — включение OTEL stats handler/интерцепторов и логгеров; по умолчанию все включены. 【F:config/config.go†L53-L76】【F:internal/observability/otel.go†L12-L60】

## Наблюдаемость
- gRPC-сервер стартует с OTEL интерцепторами при включённых метриках или трейсе и всегда использует логирующий интерцептор с выводом `correlation_id`. 【F:cmd/main.go†L69-L109】【F:internal/observability/otel.go†L62-L97】
- Корреляция берётся из поля запроса `correlation_id` или метаданных `x-correlation-id` и прокидывается в заголовки ответов. 【F:cmd/main.go†L111-L143】【F:internal/transport/grpcserver/server.go†L92-L115】

## Локальный запуск
1. Подготовьте `config/config.yaml` или укажите путь через `CONFIG_PATH`; задайте доступы MinIO, PostgreSQL (`processing.dsn`) и ARN для webhook. Без ARN или DSN сервис не стартует. 【F:cmd/main.go†L12-L83】
2. MinIO и бакеты будут созданы автоматически при запуске (при наличии прав), дополнительная ручная подготовка не требуется. 【F:internal/storage/minio_service.go†L213-L262】
3. Поднимите PostgreSQL с таблицами `processing_jobs`, `job_events`, `documents`, `document_status_logs` — они используются воркером. 【F:internal/infrastructure/persistence/processing_job_repository.go†L18-L93】
4. Сгенерируйте gRPC-код при необходимости (`proto` каталог) и запустите сервис: `go run ./cmd`. В одном процессе стартуют gRPC-сервер и фоновой воркер, корректное завершение осуществляется по сигналу. 【F:cmd/main.go†L12-L109】

## Тесты
Запуск всех тестов модуля: `go test ./...`. 【F:test.md†L4-L7】
