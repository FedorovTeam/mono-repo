# Document Service

Сервис управляет метаданными документов, версиями и статусами на пути загрузки DOCX → Markdown → GitHub. Хранит ссылки на объекты, загруженные через gRPC API `storage-service`, и предоставляет собственный gRPC API для создания, чтения, перечисления и обновления статуса документа.

## Архитектура
- PostgreSQL — хранение метаданных, версий и логов статусов.
- Storage-service (gRPC) — запись и чтение оригиналов/Markdown через методы `UploadOriginal`/`UploadMarkdown`/`GetObject`, сервис хранит только ключи.
- gRPC — внутренний API `proto/backend/document/document.proto`.

## База данных и очередь задач
- `documents` — источник правды по статусу документа и метаданным публикации.
- `processing_jobs` — очередь задач для крон-воркеров: `task_id` (UUID), `document_id`, `stage`, `status`, счётчик `attempts`, расписание `next_run_at`, произвольный `payload`.
- `job_events` — аудит выполнения задач, фиксирует смену статуса, сообщение ошибки и полезную нагрузку.
- Индексы `idx_processing_jobs_stage_status_next_run` и `idx_processing_jobs_next_run` поддерживают выборку готовых задач, `SELECT ... FOR UPDATE SKIP LOCKED` защищает от двойного захвата при нескольких воркерах.

## Статусы и повторная обработка
- Жизненный цикл документа фиксируется в статусах `UPLOADED → STORED_ORIGINAL → CONVERTED_TO_MARKDOWN → PUBLISHED_TO_GITHUB` с отдельным состоянием `FAILED`. Источник правды — таблица `documents`.
- Каждая стадия обработки («сохранить оригинал», «конвертировать», «опубликовать в GitHub») заводится в очереди `processing_jobs` со стадией (`job_stage`) и статусом (`job_status`), а все попытки фиксируются в `job_events`.
- Крон-воркеры выбирают готовые к исполнению задачи через `SELECT ... FOR UPDATE SKIP LOCKED` с фильтром по `status = 'pending'` и `next_run_at <= now()`, опираясь на индексы `idx_processing_jobs_stage_status_next_run` и `idx_processing_jobs_next_run`.
- При успешном завершении задача переводится в `status = 'completed'`, документ в `documents` переключается на следующий статус. При ошибках увеличивается `attempts`, назначается новый `next_run_at`, а деталь ошибки пишется в `job_events` и `documents.error_reason`.
- Повторная обработка доступна за счёт перевода документа в предыдущий статус и постановки новой задачи с той же парой `document_id + stage` (уникальный ключ в `processing_jobs` не даёт раздувать очередь дублями). Если документ застрял в `UPLOADED`, периодический reconciler перечитывает объект через `storage-service` и перезапускает цепочку.

## Формат ключей
Ключи формируются на стороне `storage-service` по шаблону `org/<org_slug>/project/<project_slug>/year=YYYY/month=MM/day=DD/<document_id>.<ext>` (оригинал) и `.../<document_id>.md` (Markdown). Document-service хранит только возвращённые бакеты и ключи, не работая напрямую с MinIO.

## Конфигурация
Все обязательные параметры находятся в `config.yaml` (путь передается через `CONFIG_PATH`). Значения по умолчанию настроены под инфраструктурные домены и DNS-шаблон `<context>.backend.svc.cluster.local`:
- `server.addr` — адрес gRPC сервера (по умолчанию `:50051`).
- `postgres.dsn` — строка подключения к PostgreSQL.
- `storage.original_bucket` / `storage.markdown_bucket` — бакеты, которые возвращает `storage-service` при загрузке оригинала и Markdown.
- `reconciler.interval` / `reconciler.batch_size` / `reconciler.min_uploaded_age` — периодичность и размер выборки задач для подтверждения загрузок, минимальный возраст загруженного объекта перед повторной проверкой (проверка выполняется через `storage-service.GetObject`).
- `github_retryer.interval` / `github_retryer.batch_size` / `github_retryer.max_attempts` / `github_retryer.initial_backoff` / `github_retryer.max_backoff` — параметры крон-ретраев публикации в GitHub.
- `job_follower.interval` / `job_follower.batch_size` / `job_follower.reschedule_after` — периодичность чтения завершённых задач и задержка перед повторным запуском следующей стадии.
- `services.storage_service` — `storage.backend.svc.cluster.local:50052`, `services.transformer_service` — `docling-transformer-service.docling-transformer-service.svc.cluster.local:9090` (gRPC `ConvertDocumentToMarkdown`/`ConvertDocxToMarkdown`), `services.github_publisher_service` — `github-publisher.backend.svc.cluster.local:9097`.
- `services.grpc` — общие параметры подключения к gRPC клиентам: `dial_timeout` для `grpc.WithBlock`, `min_connect_timeout` и `backoff` для `grpc.WithConnectParams`, `keepalive` для `grpc.WithKeepaliveParams` (все значения задаются в формате `time.Duration`).
- `github.repository` — целевой репозиторий (по умолчанию `FedorovTeam/knowledge-base`), `github.branch` — рабочая ветка. На входе сервис ожидает раздельные `owner` и `repository`; если в `repository` передан формат `owner/repo`, владелец выделяется автоматически, а в базе сохраняется только имя репозитория без префикса владельца.
- `observability.logs.enabled` — включает вывод стандартных логов; при `false` логгер работает в no-op режиме.
- `observability.traces.enabled` / `observability.metrics.enabled` — включает экспорт OTLP-трейсов и метрик через gRPC. Для работы требуется доступный OTLP Collector (адрес берется из `OTEL_EXPORTER_OTLP_ENDPOINT`, по умолчанию `localhost:4317` с нешифрованным соединением).

## Наблюдаемость
Периодические воркеры публикуют метрики очереди и ошибок: gauge `document_service_upload_reconciler_queue_size`/`document_service_publish_retrier_queue_size`/`document_service_job_follower_queue_size`, гистограммы размеров пачек (`document_service_*_batch_size`) и длительности (`document_service_*_duration_ms`), счётчики ошибок (`document_service_*_errors_total`). В логах фиксируются некорректные выборки, пропуски объектов и ошибки публикации/маршрутизации, чтобы отслеживать частоту сбоев на каждой стадии.

## Быстрый старт
1. Запустите PostgreSQL, `storage-service` и MinIO (см. `infrastructure.md`).
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
