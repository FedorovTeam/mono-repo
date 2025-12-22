# Document Service

Сервис хранит метаданные документов, версии и статусы цепочки `Uploaded → StoredOriginal → (preprocess_to_docx?) → ConvertedToMarkdown → PublishedToGitHub`, а выполнение этапов поручает внешним gRPC‑сервисам (storage, transformer, GitHub publisher). В базе фиксируются только ссылки и история статусов; бинарные данные не хранятся.

## gRPC API
- Контракт: `proto/backend/document/v1/document.proto`, сервер принимает `x-correlation-id` в метаданных и пишет его в логи. `CreateDocumentRequest`/`ConfirmUploadRequest` содержат `correlation_id`, но бизнес-логика его не использует — корреляция ведётся по `workflow_id=document_id`. 【F:internal/interfaces/grpc/server.go†L17-L106】【F:internal/usecase/document/create_document.go†L55-L105】
- `CreateDocument`:
  - Генерирует UUID при отсутствии `id`; валидирует формат переданного UUID.
  - Нормализует `repository`: допускается `owner/repo`, конфликт с явным `owner` запрещён; дефолты `owner`/`repository`/`branch`/`base_branch` и бакетов берутся из конфигурации, `base_branch` заполняется из `branch`, если пусто.
  - `path` по умолчанию `knowledge/docs/<document_id>.md`; `upload_object_key` и `markdown_object_key` обязательны и сохраняются как есть.
  - Создаёт документ со статусом `Uploaded`, первой версией №1 и логом статуса; `workflow_id` равен `document_id`. 【F:internal/usecase/document/create_document.go†L22-L110】
- `ConfirmUpload`:
  - Валидирует UUID, пустые значения bucket/object_key и совпадение с сохранёнными в документе.
  - Обязательно проверяет наличие объекта через `storage-service.GetObject`; отсутствие gRPC‑клиента или объект в хранилище даёт ошибку.
  - Ставит задачу `processing_jobs` со стадией `store_original` в статусе `pending`, добавляет событие `scheduled`; статус документа не меняет. Ошибка постановки помечает документ `Failed`. 【F:internal/usecase/document/confirm_upload.go†L20-L88】
- `UpdateDocumentStatus`:
  - Допустимые переходы: линейные стадии, повторная установка текущего статуса и выход из `Failed` в рабочие стадии; `Failed` заполняет `error_reason`.
  - Переданные `version`/`original_object_key`/`markdown_object_key` создают или обновляют запись в `document_versions` (проверяется совпадение ключей для существующей версии) и при необходимости переносят ключи в документ.
  - После `StoredOriginal` и `ConvertedToMarkdown` автоматически планирует следующую задачу (по роутеру: `preprocess_to_docx` или `convert_to_markdown`, далее `publish_to_github`), иначе возвращает активную задачу, если она была. 【F:internal/usecase/document/update_status.go†L16-L92】
- `GetDocument` и `ListDocuments` возвращают документ с версиями и `job_id` активной задачи; список сортируется по `created_at DESC` и фильтруется по статусу. 【F:internal/interfaces/grpc/server.go†L59-L87】【F:internal/infrastructure/postgres/repository.go†L108-L178】

## Модель данных (PostgreSQL)
- `documents`: `id`, `name`, `owner`, `repository`, `branch`, `base_branch`, `path`, `status`, `workflow_id`, `original_bucket`/`object_key`, `markdown_bucket`/`object_key`, `error_reason`, `create_pr`, `created_at/updated_at`; уникальные индексы на пары bucket/object_key. 【F:db/migrations/001_create_documents.sql†L11-L54】【F:db/migrations/003_add_repository_and_error_fields.sql†L1-L15】
- `document_versions`: версия с ключами и `markdown_queue_id`; уникально по `(document_id, version)`. 【F:db/migrations/001_create_documents.sql†L37-L49】【F:db/migrations/005_add_markdown_queue_id.sql†L1-L4】
- `document_status_logs`: история переходов с `workflow_id` и `reason`; используется для вычисления фактического статуса при `Failed`. 【F:db/migrations/001_create_documents.sql†L51-L64】【F:internal/service/document/status_application.go†L49-L76】
- `processing_jobs`: очередь этапов (`stage` = `store_original`/`preprocess_to_docx`/`convert_to_markdown`/`publish_to_github`; `status` = `pending`/`in_progress`/`completed`/`failed`), `attempts`, `next_run_at`, `payload` (JSON с ссылками и вспомогательными полями), уникальность по `(document_id, stage)`; индексы `idx_processing_jobs_stage_status_next_run`, `idx_processing_jobs_next_run`. `preprocess_to_docx` добавлен через миграцию `007_add_preprocess_stage.sql`. 【F:db/migrations/006_add_processing_jobs.sql†L9-L51】【F:db/migrations/007_add_preprocess_stage.sql†L1-L16】
- `job_events`: аудит задач с `event_type` (`scheduled`/`postponed`/`completed`/`failed`/`recovered`/`handled`), `message` и `payload` (например, `last_error`, `queue_id`, `commit_sha`). 【F:db/migrations/006_add_processing_jobs.sql†L53-L71】

## Очередь и фоновые воркеры
- Все воркеры и gRPC‑сервер запускаются общим раннером; ошибка любого воркера останавливает остальные по контексту. 【F:cmd/main.go†L118-L186】【F:internal/service/document/runner.go†L18-L47】
- `UploadReconciler`: периодически ищет документы в статусе `Uploaded`, старше `min_uploaded_age`, с заполненными ключами, и повторно вызывает `ConfirmUpload`. 【F:internal/service/document/reconciler.go†L15-L74】
- `StageWorker` (по стадиям):
  - Выбирает `pending` задачи `FOR UPDATE SKIP LOCKED`, помечает `in_progress`, инкрементирует `attempts`; для конвертации обрабатывает одну задачу за цикл (`convert_batch_size=1`).
  - `store_original`: idempotent переход в `StoredOriginal`, если документ реально в `Uploaded` и у него есть ключи оригинала. 【F:internal/service/document/stage_worker.go†L49-L116】
- `preprocess_to_docx`: формирует payload (bucket/object_key/target_object_key, корреляция), создаёт задачу в preprocess-service и либо завершает стадию, либо откладывает её с задержкой `preprocess_retry_delay`; временные gRPC‑ошибки используют экспоненциальный backoff от того же интервала. Статус `FAILED` фиксирует ошибку и завершает задачу. Значение 15s в примере конфигурации согласовано с `poll_interval=2s` preprocess-service, поэтому небольшие документы не ждут минутных пауз. 【F:internal/service/document/stage_worker.go†L79-L215】【F:config/config.yaml†L43-L51】
- `convert_to_markdown`: собирает payload (bucket/object_key/расширение файла, correlation_id из `workflow_id`), поддерживает `preprocess_job_id` и `preprocessed_bucket/object_key/target_object_key` и при их наличии использует результат предобработки как источник вместо оригинала, вызывает `TransformerService.ConvertDocumentToMarkdown` с `wait_for_result=false`. Статусы `done` или `ConvertedToMarkdown` завершают задачу и переводят документ в `ConvertedToMarkdown` с новой версией и `queue_id`; используемый источник фиксируется в `DocumentVersion.OriginalObjectKey` и `job_event` (`source_bucket`, `source_object_key`, `preprocess_job_id`). Промежуточные статусы откладывают задачу на `retry_delay`. Временные gRPC‑коды (`DeadlineExceeded`, `Unavailable`, `Internal`, `Aborted`, `ResourceExhausted`) дают экспоненциальный backoff (`retry_delay * 2^attempts`, максимум 5 попыток, далее документ помечается `Failed`). 【F:internal/service/document/stage_worker.go†L118-L215】【F:internal/service/document/activities.go†L83-L161】
  - `publish_to_github`: дополняет payload данными документа и дефолтами коммита/PR, требует непустые `commit_message`, `author_name`, `author_email`; формирует `idempotency_key=publish:<document_id>:<branch>:<markdown_key>`. Успешный RPC переводит документ в `PublishedToGitHub`; ответ с `retry_after` или временные gRPC‑коды откладывают задачу с backoff, остальные ошибки фиксируют `failed`. 【F:internal/service/document/stage_worker.go†L217-L315】【F:internal/service/document/activities.go†L170-L255】
- `JobFollower`: читает `completed`/`failed` задачи (после `reschedule_after`), переносит `next_run_at`, на успехе ставит следующую стадию. После `store_original` выбор зависит от роутера: `doc`/`pdf` при `router.preprocess_enabled=true` идут в `preprocess_to_docx`, `docx` и все остальные — сразу в `convert_to_markdown`. Завершение `preprocess_to_docx` ставит `convert_to_markdown`, подставляя в payload `preprocessed_bucket/object_key/target_object_key` и `preprocess_job_id`, если они присутствуют. Ошибки стадий помечают документ `Failed` и фиксируют событие `handled`. 【F:internal/service/document/job_follower.go†L17-L187】
- `ProcessingReconciler`: возвращает зависшие `in_progress` в `pending`, если они старше `stage_interval + grace_period`, увеличивает `attempts`, пишет событие `recovered`; имеет до 3 попыток с экспоненциальным backoff и джиттером при ошибках репозитория. 【F:internal/service/document/processing_reconciler.go†L15-L156】

## Интеграции
- **Storage-service:** используется только для проверки наличия объекта через `GetObject` в `ConfirmUpload` и `UploadReconciler`; прямых вызовов MinIO нет. 【F:internal/usecase/document/confirm_upload.go†L57-L88】【F:internal/infrastructure/storage/storage.go†L9-L29】
- **Transformer-service:** `ConvertDocumentToMarkdown` вызывается без ожидания результата, передаются buckets/keys, расширение файла и `correlation_id` (из заголовков или `workflow_id`). Ошибочные статусы ответа (`failed`/`error`) интерпретируются как `FailedPrecondition`. 【F:internal/service/document/activities.go†L107-L161】
- **Github-publisher-service:** `PublishMarkdown` получает repo/branch/path, ключ Markdown из MinIO, commit/PR данные и `idempotency_key`; сохраняемые в `job_events` поля включают `commit_sha`, `pull_request_number/url`, `retry_after`, `idempotency_hit`. Ответы `TEMPORARY_FAILURE` или с `retry_after` считаются временной ошибкой. 【F:proto/backend/githubpublisher/v1/githubpublisher.proto†L28-L77】【F:internal/service/document/stage_worker.go†L241-L315】

## Конфигурация
- Файл YAML передаётся флагом `-config` (по умолчанию `config/config.yaml`); миграции всегда применяются при старте, флаг `-migrate-only` завершает процесс после миграций. 【F:cmd/main.go†L20-L70】
- Обязательные поля: `server.addr`, `postgres.dsn`, `storage.original_bucket/markdown_bucket`, адреса сервисов `services.storage_service`/`transformer_service`/`github_publisher_service`, `grpc_clients` (timeout/keepalive/backoff), шаблон `github.commit_message`, автор `github.author_name/email`, интервалы `reconciler`, `job_follower`, `stage_worker` (interval/batch_size/convert_batch_size/retry_delay/preprocess_retry_delay, `grace_period` ≥ 0). `preprocess_retry_delay` можно не указывать — по умолчанию берётся из `retry_delay`. Пустые значения приводят к ошибке валидации. 【F:pkg/config/config.go†L13-L114】
- Дополнительные параметры: `github.owner/repository/branch/base_branch/base_url`, `github.pr_title/pr_body`, `github.base_branch` по умолчанию подставляется в `CreateDocument`; `stage_worker.wait_markdown` присутствует в конфигурации, но сейчас не используется в коде. Роутер (`router`) управляет стадией после сохранения оригинала: `preprocess_enabled`, список расширений `preprocess_extensions` (по умолчанию `doc`/`pdf`), репозитарные переопределения (`repositories[].owner/repository/preprocess_enabled/preprocess_extensions`). 【F:pkg/config/config.go†L113-L164】【F:config/config.yaml†L33-L53】
- gRPC‑клиенты создаются с insecure‑транспортом, `keepalive` и `backoff` из `grpc_clients`. 【F:internal/infrastructure/grpcclient/client.go†L12-L45】

## Наблюдаемость
- Логи можно отключить (`observability.logs.enabled=false`); трейсинг и метрики включаются отдельными флагами и экспортируются в OTLP по gRPC (адрес и креды берутся из стандартных переменных OTEL). 【F:pkg/observability/setup.go†L11-L117】
- Метрики воркеров: gauge `document_service_<worker>_queue_size`, гистограммы `..._batch_size` и `..._duration_ms`, счётчики `..._errors_total`, `..._retries_total`, `..._final_errors_total`; регистрируются через OpenTelemetry meter `document-service/<worker>`. 【F:internal/service/document/metrics.go†L12-L70】
- Корреляционный ID прокидывается в исходящие gRPC вызовы (`x-correlation-id`) при наличии в контексте или из `workflow_id`; сервер логирует полученный ID даже если он отсутствует. 【F:internal/service/document/activities.go†L28-L52】【F:internal/interfaces/grpc/server.go†L114-L134】

## Локальный запуск
1. Поднимите PostgreSQL и запустите зависимости (storage-service, docling-transformer-service, github-publisher-service), как показано в `infrastructure.md`.
2. Примените миграции (выполняются автоматически, но можно отдельно): `go run ./cmd/main.go --config config/config.yaml --migrate-only`.
3. Сгенерируйте gRPC-код после правок proto: `cd proto && ./generate.sh`.
4. Запустите сервис: `go run ./cmd/main.go --config config/config.yaml`.
5. Примеры grpcurl‑запросов — в `test.md`.

## Структура каталога
- `cmd/main.go` — точка входа, старт gRPC-сервера и всех воркеров.
- `internal/` — доменные модели, use case, фоновые воркеры, инфраструктурные адаптеры.
- `proto/backend/*` — gRPC контракты и сгенерированный код.
- `db/migrations` — SQL миграции (применяются через embedded runner).
- Документация: `code.md`, `flows.md`, `sequence.md`, `infrastructure.md`, `usecases.md`, `diagrams.md`, `contracts.md`, `tasks.md`, `test.md`.
