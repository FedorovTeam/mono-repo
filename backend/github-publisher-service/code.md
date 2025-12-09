# Code view

Диаграмма отражает основные модули и их связи в актуальном коде `github-publisher-service`.

```mermaid
classDiagram
    direction LR

    class ConfigLoader {
      +Load() *Config
    }

    class Observability {
      +Setup(ctx, cfg) Providers
      +HTTPTransport(providers, base)
      +GRPCServerOptions(providers)
    }

    class MinioMarkdownLoader {
      +LoadMarkdown(ctx, bucket, key) []byte
    }

    class GithubClient {
      +CommitMarkdown(ctx, path, content, msg, branch, corrID, authorName, authorEmail)
      +CreatePullRequest(ctx, branch, baseBranch, title, body, corrID)
    }

    class PublisherServer {
      +PublishMarkdown(ctx, req) (resp, error)
      -pickCorrelationID(ctx, explicit) string
    }

    class GRPCServer {
      +Serve()
    }

    ConfigLoader --> Observability : читает config.yaml
    ConfigLoader --> MinioMarkdownLoader : выдаёт MinIO настройки
    ConfigLoader --> PublisherServer : GitHub/MinIO/GRPC параметры
    Observability --> GithubClient : otel HTTP transport
    Observability --> GRPCServer : otel stats handler
    MinioMarkdownLoader --> PublisherServer : загружает Markdown при пустом content
    GithubClient --> PublisherServer
    PublisherServer --> GRPCServer : реализует PublishMarkdown RPC
    ConfigLoader --> GRPCServer : порт GRPC
```

Основные моменты, отражённые на схеме:

- `cmd/main.go` загружает конфигурацию, инициализирует OpenTelemetry провайдеры, создаёт MinIO‑клиент для загрузки Markdown и HTTP‑клиент с otel-транспортом, затем регистрирует gRPC‑сервер `GithubPublisherService` на указанном порту.
- `PublisherServer` валидирует входные поля, подставляет owner/repository/branch/baseBranch из конфигурации, подтягивает Markdown из MinIO, если `content` пуст, публикует коммит и при необходимости открывает Pull Request.
- Корреляционный идентификатор берётся либо из запроса, либо из входящих gRPC‑метаданных с заголовком из конфигурации (`X-Correlation-ID` по умолчанию) и передаётся GitHub API через заголовок.
