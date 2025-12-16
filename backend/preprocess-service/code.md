# Обзор кода

```mermaid
flowchart TD
    main[cmd/preprocess-service/main.go]
    cfg[internal/config]
    app[internal/app]
    domain[internal/domain]
    transport[internal/transport]

    main --> cfg
    main --> app
    app --> cfg
    app --> transport
    transport --> domain
```

- `main` отвечает за загрузку конфигурации и корректное завершение по сигналам.
- `config` инкапсулирует чтение YAML и структуру секции `preprocess`.
- `app` связывает доменные и транспортные слои.
- `transport` предоставляет health‑endpoint и готов к расширению gRPC/HTTP API.
- `domain` хранит сущности и базовую валидацию заданий на обработку.
