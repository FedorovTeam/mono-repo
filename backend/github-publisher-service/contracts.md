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
- `content` — содержимое Markdown (обязательно).
- `commit_message` — сообщение коммита (обязательно).
- `create_pr` — открыть ли PR.
- `pr_title`, `pr_body` — параметры PR.
- `correlation_id` — явное значение заголовка трассировки (если не передан — берётся из метаданных gRPC).

### PublishMarkdownResponse
- `commit_sha` — SHA созданного коммита.
- `pull_request_number` и `pull_request_url` — если был открыт PR.
