# Обзор кода

```mermaid
flowchart TD
    main[cmd/preprocess-service/main.go]
    cfg[internal/config]
    app[internal/app]
    domain[internal/domain]
    transport[internal/transport]
    db[(PostgreSQL: preprocess_queue)]

    main --> cfg
    main --> app
    app --> cfg
    app --> transport
    transport --> domain
    domain --> db
```

- `main` отвечает за загрузку конфигурации и корректное завершение по сигналам.
- `config` инкапсулирует чтение YAML и структуру секции `preprocess`.
- `app` связывает доменные и транспортные слои.
- `transport` предоставляет health‑endpoint и готов к расширению gRPC/HTTP API.
- `domain` хранит сущности и базовую валидацию заданий на обработку.
- `preprocess_queue` в PostgreSQL предназначена для идемпотентной постановки заданий предобработки; индексируется по статусу и
  расписанию исполнения, а также по исходным объектам в MinIO.
