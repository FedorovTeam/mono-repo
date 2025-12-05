# storage-service

Сервис отвечает за хранение исходных документов и их Markdown-версий в приватных бакетах MinIO. Публичный контракт описан в `proto/backend/storage/v1/storage.proto`.

## Назначение
- Прием исходных файлов (knowledge-docx) и Markdown (knowledge-md).
- Генерация ключей по шаблону `org/<org_slug>/project/<project_slug>/year=YYYY/month=MM/day=DD/<document_id>.<ext>`.
- Для выдачи presigned ссылок используются дефолтные `org_slug=knowledge` и `project_slug=documents`, чтобы сохранять единый путь размещения объектов.
- Выдача сохраненных объектов по ключу.
- Выдача presigned PUT ссылки через RPC `GenerateUploadURL` (получает `document_id`, `file_name`, `content_length`, `correlation_id`, `expires_in_seconds`) с установкой `x-amz-meta-correlation-id` в MinIO.

## Конфигурация
Обязательные параметры задаются в `config/config.yaml` и читаются через `CONFIG_PATH`:
- `service.grpc_port` — порт gRPC-сервера.
- `minio.endpoint` — `api.minio.tech.fedorov-team.tech`, `minio.access_key` / `minio.secret_key` — ключи доступа, `minio.use_ssl=true`.
- `minio.buckets.knowledge_docx` — приватный бакет для оригиналов и загрузки через signed URL.
- `minio.buckets.knowledge_md` — приватный бакет для Markdown.

## Локальный запуск
1. Поднимите MinIO (например, через `docker-compose` шаблона template-service) и создайте бакеты `knowledge-docx` и `knowledge-md` с приватной политикой.
2. Обновите `config/config.yaml` при необходимости.
3. Сгенерируйте gRPC-код при помощи `protoc` (директория `proto`).
4. Запустите сервис: `go run ./cmd`.
   В логах появится инициализация MinIO и бакетов, а при gRPC-запросах — correlation-id из поля запроса или метаданных `x-correlation-id`. Завершение по `Ctrl+C` останавливает сервер graceful shutdown'ом.

## Тесты
В модуле предусмотрены unit-тесты для генерации ключей и взаимодействия с MinIO через mock-клиент: `go test ./...`.
