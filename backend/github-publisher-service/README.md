# Github Publisher Service

Сервис публикует Markdown-файлы в GitHub-репозиторий через gRPC-метод `PublishMarkdown`. Он коммитит содержимое в указанную ветку, при необходимости открывает Pull Request, прокидывает `correlation-id` во все запросы к GitHub API и поддерживает идемпотентные повторы с рекомендациями по бэкоффу (`retry_after`). Фоновый воркер обработки статусов запускается внутри основного бинарника и двигает задачи стадии `publish_to_github` из таблицы `processing_jobs`.

## Быстрый старт
1. Сформируйте конфигурацию `config/config.yaml` с токеном GitHub, параметрами репозитория и доступом к MinIO.
2. Сгенерируйте gRPC код: `./proto/generate.sh`.
3. Запустите сервис (gRPC + фоновый воркер в одном бинарнике): `go run ./cmd/main.go` (порт настраивается в конфиге).
4. Выполните тесты: `go test ./...`.
5. Примеры ручных сценариев см. в `test.md`.

## Конфигурация
Все параметры читаются из `config/config.yaml` (можно переопределить через `CONFIG_PATH`). Обязательные поля:
- `service.name` / `service.grpc_port` — идентификатор и порт gRPC сервера.
- `github.base_url` — `https://api.github.com`.
- `github.owner` и `github.repository` — организация и репозиторий (по умолчанию `FedorovTeam/knowledge-base`).
- `github.token` — токен с правами на коммиты/PR.
- `github.default_branch` — целевая ветка (по умолчанию `main`).
- `github.author_name` / `github.author_email` — метаданные коммита.
- `github.correlation_header` — заголовок для прокидывания correlation-id.
- `minio.*` — endpoint, ключи доступа и флаг `secure` для скачивания Markdown по bucket/key.
- `processing.dsn` — строка подключения к PostgreSQL с таблицами `processing_jobs`/`documents`.
- `processing.stage` — стадия, которую обрабатывает воркер (по умолчанию `publish_to_github`).
- `processing.poll_interval` / `processing.max_attempts` / `processing.backoff` — интервалы опроса, максимальное число попыток и рост задержки перед ретраями.
- `observability.otlp_endpoint` — адрес OTLP-коллектора для отдачи метрик и трейсов.
- `observability.metrics_enabled` / `observability.traces_enabled` / `observability.logs_enabled` — отдельные флаги включения экспорта. При выключении используются no-op провайдеры, HTTP/gRPC остаются без внешних отправок, а логи можно полностью заглушить.

## Структура
- `cmd/main.go` — единая точка входа: gRPC сервер + фоновый воркер публикации.
- `internal/config` — загрузка конфигурации из YAML.
- `internal/github` — клиент GitHub с поддержкой коммитов и Pull Request.
- `internal/service` — реализация gRPC `GithubPublisherService`.
- `proto/backend/githubpublisher/v1` — gRPC контракт.
- Документация: `flows.md`, `sequence.md`, `usecases.md`, `code.md`, `infrastructure.md`, `diagrams.md`, `test.md`.
