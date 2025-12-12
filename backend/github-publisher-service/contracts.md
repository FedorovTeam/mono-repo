# Контракты

## gRPC
- Файл: `proto/backend/githubpublisher/v1/githubpublisher.proto`
- Пакет: `backend.githubpublisher.v1`
- Метод: `PublishMarkdown(PublishMarkdownRequest) returns (PublishMarkdownResponse)`

### PublishMarkdownRequest
- `owner`, `repository` — опционально, по умолчанию берутся из `config.yaml`.
- `branch` — ветка для коммита, по умолчанию `default_branch`.
- `base_branch` — целевая ветка PR.
- `path` — путь Markdown файла в репозитории (обязателен).
- `content` — содержимое Markdown (обязательно), либо `bucket` + `object_key` для чтения из MinIO.
- `commit_message` — сообщение коммита (опционально, при пустом значении сформируется из пути).
- `create_pr` — открыть ли PR.
- `pr_title`, `pr_body` — параметры PR.
- `correlation_id` — явное значение заголовка трассировки (если не передан — берётся из метаданных gRPC).
- `idempotency_key` — ключ идемпотентности; повторный вызов с тем же ключом и успешной публикацией вернёт кэшированный ответ.
- `retry_policy` — базовый и максимальный backoff (временные интервалы), а также предел попыток, по которым сервис сам выполнит несколько попыток публикации до возвращения ответа.

### PublishMarkdownResponse
- `status` — нормализованный итог операции: `SUCCESS`, `TEMPORARY_FAILURE`, `PERMANENT_FAILURE`.
- `commit_sha` — SHA созданного коммита (для успешных вызовов или если PR не создался, но коммит уже записан).
- `pull_request_number` и `pull_request_url` — если был открыт PR.
- `error_code` — нормализованный код ошибки GitHub (`TIMEOUT`, `RATE_LIMIT`, `SERVER_ERROR`, `CONFLICT`, `PERMISSION`, `NETWORK`, `NOT_FOUND`, `UNKNOWN`).
- `error_message` — текстовое описание ошибки GitHub/HTTP, пригодное для логов.
- `retry_after` — рекомендованная задержка для следующей попытки при временной ошибке, вычисленная с учётом внутренних ретраев и `retry_policy` (заполнена даже после исчерпания попыток).
- `idempotency_hit` — `true`, если результат взят из кэша по `idempotency_key` без повторной публикации.
