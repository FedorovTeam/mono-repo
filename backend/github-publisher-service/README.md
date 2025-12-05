# Github Publisher Service

Сервис публикует Markdown-файлы в GitHub-репозиторий через gRPC-метод `PublishMarkdown`. Он коммитит содержимое в указанную ветку, при необходимости открывает Pull Request и прокидывает `correlation-id` во все запросы к GitHub API.

## Быстрый старт
1. Сформируйте конфигурацию `config/config.yaml` с токеном GitHub и параметрами репозитория.
2. Сгенерируйте gRPC код: `./proto/generate.sh`.
3. Запустите сервис: `go run ./cmd/server` (порт настраивается в конфиге).
4. Выполните тесты: `go test ./...`.
5. Примеры ручных сценариев см. в `test.md`.

## Структура
- `cmd/server` — точка входа gRPC сервера.
- `internal/config` — загрузка конфигурации из YAML.
- `internal/github` — клиент GitHub с поддержкой коммитов и Pull Request.
- `internal/service` — реализация gRPC `GithubPublisherService`.
- `proto/backend/githubpublisher/v1` — gRPC контракт.
- Документация: `flows.md`, `sequence.md`, `usecases.md`, `code.md`, `infrastructure.md`, `diagrams.md`, `test.md`.
