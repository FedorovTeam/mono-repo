# Github Publisher Service

Сервис публикует Markdown‑файлы в GitHub через единственный gRPC‑метод `PublishMarkdown`: он коммитит содержимое в нужную ветку, при флаге `create_pr` создаёт Pull Request, прокидывает `correlation-id` в запросы GitHub API и классифицирует ошибки для расчёта `status`/`retry_after`. Корректная работа подтверждается только по коду сервиса и актуальной документации (`code.md`, `flows.md`, `sequence.md`, `usecases.md`, `infrastructure.md`). Очередь `publish_to_github` в таблице `processing_jobs` обслуживается stage worker `publish_to_github` в `document-service`; данный сервис вызывается только по gRPC.

## Возможности
- Получение контента напрямую из запроса или через MinIO (`bucket` + `object_key`) с автодетекцией кодировки и переводом в UTF‑8.
- Разрешение `owner`/`repository`/`branch`/`base_branch` из запроса либо из конфигурации (конфиг должен содержать реальные значения, иначе поля останутся пустыми).
- Идемпотентность по `idempotency_key`: повтор успешного запроса возвращает кэшированный ответ с `idempotency_hit=true` без повторных HTTP‑вызовов GitHub (кэш процессный, хранит только успехи).
- Внутренние ретраи с экспоненциальным бэкоффом по `retry_policy` (из запроса или конфигурации) и учётом внешнего счётчика `attempt`; временные ошибки возвращают `TEMPORARY_FAILURE` и `retry_after`.
- Корреляционный идентификатор берётся из поля `correlation_id` или gRPC‑метаданных по заголовку из конфигурации (по умолчанию `X-Correlation-ID`) и передаётся в вызовы GitHub.
- Обработка очереди находится в `document-service` (stage worker `publish_to_github`); `github-publisher-service` предоставляет только gRPC RPC `PublishMarkdown` и не опрашивает БД с задачами.

## gRPC API
- Контракт: `proto/backend/githubpublisher/v1/githubpublisher.proto`, сервис `GithubPublisherService` с методом `PublishMarkdown`.
- Запрос `PublishMarkdownRequest`:
  - Обязателен `path`.
  - `owner`/`repository`/`branch`/`base_branch`/`commit_message`/`author_*`/`correlation_id`/`pr_*`/`create_pr`/`idempotency_key`/`attempt`/`retry_policy` — опциональны. `repository` в формате `owner/repo` валидируется: конфликт с явным `owner` или пустые части приводят к `INVALID_ARGUMENT`.
  - Если `content` пуст, обязательны `bucket` и `object_key`; контент скачивается из MinIO и конвертируется в UTF‑8 (BOM/UTF‑16/автоопределение через `charsetautodetect`), иначе возвращается `INVALID_ARGUMENT`.
  - Автор коммита берётся из запроса или из конфигурации; при пустом `commit_message` подставляется `Update <path>`/`Update markdown content`.
  - Параметры веток заполняются из `github.default_branch`, если не заданы (если в конфиге пусто, останутся пустыми и в запросах к GitHub).
- Ответ `PublishMarkdownResponse`:
  - `status`: `SUCCESS`, `TEMPORARY_FAILURE`, `PERMANENT_FAILURE`.
  - `commit_sha`, `pull_request_number/url` (если PR создавался), `error_code/message`, `retry_after`, `idempotency_hit`.
  - gRPC‑коды: `INVALID_ARGUMENT` (валидация пути/репозитория/обязательных параметров/кодировки), `FAILED_PRECONDITION` (нет клиента MinIO или ошибка загрузки), прочие — `INTERNAL`.
- Взаимодействие с GitHub: GET `/repos/{owner}/{repo}/contents/{path}?ref=branch` для чтения SHA/контента (при совпадении контента возвращается существующий SHA без PUT); PUT `/repos/{owner}/{repo}/contents/{path}` с Base64‑контентом, `branch` и `sha` (для обновления) и автором коммита; POST `/repos/{owner}/{repo}/pulls` при `create_pr=true`. Корреляционный заголовок передаётся как задано в конфиге.
- Ошибки классифицируются: 401/403 → `PERMISSION`, 404 → `NOT_FOUND`, 409 → `CONFLICT`, 429/5xx/таймауты/сетевые → `TEMPORARY_FAILURE` с `retry_after`, остальное → `PERMANENT_FAILURE/UNKNOWN`. `attempt` из запроса добавляется к счётчику внутренних попыток при расчёте `retry_after`.

## Очередь и воркер
- Обработка задач очереди выполняется stage worker `publish_to_github` в `document-service`. Этот worker готовит payload (репозиторий, ветки, автор, commit/pr‑шаблоны, idempotency key) и вызывает gRPC `PublishMarkdown`, а затем переводит документ на следующий статус.

## Конфигурация
Все параметры читаются из `config/config.yaml` или из файла по пути `CONFIG_PATH`.
- `service.name`, `service.grpc_port` — идентификатор сервиса и порт gRPC.
- `github.base_url`, `github.owner`, `github.repository`, `github.token`, `github.default_branch`, `github.author_name`, `github.author_email`, `github.correlation_header` (по умолчанию `X-Correlation-ID`). Значения `owner`/`repository`/`default_branch` не имеют жёстких дефолтов и при пустых настройках попадут в запросы к GitHub как пустые.
- `minio.endpoint`, `minio.access_key`, `minio.secret_key`, `minio.secure` — подключение для загрузки Markdown.
- `retry.initial_backoff`/`max_backoff`/`max_attempts` — дефолты для внутренней стратегии публикации (подставляются, если не заданы: 5s/30s/3).
- `observability.service_name` (по умолчанию `service.name`, затем `github-publisher-service`), `observability.metrics_enabled`/`traces_enabled`/`logs_enabled` (если все три опущены или `false`, сервис включает все), `observability.otlp_endpoint` — обязателен при включённых метриках или трейсигах.

## Локальный запуск и тестирование
1. Подготовьте `config/config.yaml` или переменную `CONFIG_PATH` с настройками GitHub/MinIO/OTLP.
2. Сгенерируйте gRPC код: `./proto/generate.sh`.
3. Запустите сервис: `go run ./cmd/main.go`.
4. Выполните тесты: `go test ./...`.
5. Примеры ручного вызова см. в `test.md`.

## Зависимости и наблюдаемость
- GitHub API вызывается через `https://api.github.com` (можно изменить `github.base_url`), HTTP‑клиент имеет таймаут 15s и otel‑транспорт; заголовки включают `Authorization: Bearer <token>` и `correlation_header` из конфига.
- MinIO используется только для чтения Markdown (`GetObject`), ключ очищается от ведущего `/`.
- OTLP экспорт трейсинг/метрик работает по gRPC; при `logs_enabled: false` логгер глушится, иначе пишет в stdout.
- gRPC сервер обёрнут otel‑хендлером, HTTP‑клиент — otel‑транспортом; кастомных метрик и HTTP эндпоинтов нет.

## Структура проекта
- `cmd/main.go` — точка входа (gRPC сервер).
- `pkg/config` — загрузка и нормализация конфигурации.
- `internal/service` — адаптер gRPC `PublishMarkdown`.
- `internal/usecase` — бизнес‑логика публикации, ретраи, идемпотентность, загрузка контента.
- `internal/infrastructure/github` — HTTP‑клиент GitHub и адаптер доменного интерфейса.
- `internal/infrastructure/storage` — загрузчик Markdown из MinIO.
- `proto/backend/githubpublisher/v1` — gRPC контракт и генерация (`proto/generate.sh`).
- Документация: `code.md`, `flows.md`, `sequence.md`, `usecases.md`, `infrastructure.md`, `diagrams.md`, `test.md`.
