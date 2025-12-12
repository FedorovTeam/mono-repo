# storage-service

Сервис отвечает за хранение исходных документов и их Markdown-версий в приватных бакетах MinIO. Публичный контракт описан в `proto/backend/storage/v1/storage.proto` и включает gRPC-методы `UploadOriginal`, `UploadMarkdown`, `GetObject` и `GenerateUploadURL`.

## Назначение
- Прием исходных файлов (knowledge-original) и Markdown (knowledge-md) через gRPC (`UploadOriginal`, `UploadMarkdown`).
- Генерация ключей по шаблону `org/<org_slug>/project/<project_slug>/year=YYYY/month=MM/day=DD/<uuid>/<document_id>.<ext>`.
- Для выдачи presigned ссылок используются дефолтные `org_slug=knowledge` и `project_slug=documents`, чтобы сохранять единый путь размещения объектов.
- Выдача сохраненных объектов по ключу через RPC `GetObject`.
- Выдача presigned PUT ссылки через RPC `GenerateUploadURL` (получает `document_id`, `file_name`, `content_length`, `correlation_id`, `expires_in_seconds`) с установкой `x-amz-meta-correlation-id` в MinIO и возвратом парных ключей для оригинала и Markdown (`bucket`/`object_key`, `markdown_bucket`/`markdown_object_key`).

## Конфигурация
Обязательные параметры задаются в `config/config.yaml` и читаются через `CONFIG_PATH`:
- `service.grpc_port` — порт gRPC-сервера (единый entrypoint вместе с фоновым воркером).
- `minio.endpoint` — `api.minio.tech.fedorov-team.tech`, `minio.access_key` / `minio.secret_key` — ключи доступа, `minio.use_ssl=true`.
- `minio.buckets.knowledge_docx` — приватный бакет для оригиналов и загрузки через signed URL.
- `minio.buckets.knowledge_md` — приватный бакет для Markdown.
- `events.upload_bucket` — бакет, для которого включается webhook-уведомление о завершенной загрузке (по умолчанию `minio.buckets.knowledge_docx`).
- `events.webhook_arn` — ARN MinIO webhook/queue для доставки события `s3:ObjectCreated:*` в gateway.
- `processing.dsn` — подключение к PostgreSQL с таблицами `processing_jobs`, `job_events`, `document_status_logs`.
- `processing.stage` — стадия, которую обрабатывает воркер (`store_original` по умолчанию).
- `processing.poll_interval_seconds` / `processing.backoff_seconds` — интервалы опроса задач и backoff между попытками.
- `processing.max_attempts` — лимит попыток на одну задачу перед фиксацией статуса `failed`.
- `observability.enable_metrics` / `observability.enable_traces` / `observability.enable_logs` — включают экспорт метрик, трейсинга и логов соответственно (по умолчанию все включено).

`GenerateUploadURL` валидирует, что `content_length > 0`, `expires_in_seconds > 0`, а расширение `file_name` входит в список `doc`/`docx`/`pdf`; при формировании ссылки проверяются доступность бакета загрузки и markdown-бакета.

## Локальный запуск
1. Поднимите MinIO (например, через `docker-compose` шаблона template-service) и создайте бакеты `knowledge-original` и `knowledge-md` с приватной политикой.
2. Подготовьте подключение к PostgreSQL, в котором доступны таблицы `processing_jobs`, `job_events`, `documents`, `document_status_logs` (используются in-process воркером).
3. Обновите `config/config.yaml` при необходимости.
4. Сгенерируйте gRPC-код при помощи `protoc` (директория `proto`).
5. Запустите сервис: `go run ./cmd`. В едином процессе стартуют gRPC-сервер и фоновой воркер опроса `processing_jobs`; завершение по `Ctrl+C` останавливает оба компонента graceful shutdown'ом.
   В логах появится инициализация MinIO и бакетов, а при gRPC-запросах — correlation-id из поля запроса или метаданных `x-correlation-id`.

## Тесты
В модуле предусмотрены unit-тесты для генерации ключей и gRPC-обработчиков; выполнить их можно командой `go test ./...`.
