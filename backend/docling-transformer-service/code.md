# Code / Module view

```mermaid
graph TD
    main[cmd/server] --> cfg[internal/config]
    main --> srv[internal/server]
    srv --> conv[internal/converter]
    conv --> tel[internal/telemetry]
    srv --> proto[proto/backend/transformer/v1]
```

- `internal/server` содержит gRPC сервер и интерсептор логирования correlation-id.
- `internal/converter` знает только о HTTP Docling и не зависит от gRPC.
- `internal/telemetry` централизует работу с correlation-id.
