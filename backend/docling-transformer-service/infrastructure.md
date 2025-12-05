# Infrastructure view

```mermaid
graph LR
    subgraph App
        TR[docling-transformer-service]
    end
    TR -->|gRPC 9092| GW[Gateway/Workflows]
    TR -->|HTTP /convert\nX-Correlation-Id| PD[Pandoc Wrapper]
    TR -->|S3 API| MN[(MinIO bucket docling-transformations)]
    TR -->|Client| TMP[Temporal Cloud]
    TR -->|Logs/Tracing| OBS[OTel Collector]
```

- gRPC порт читается из `service.grpc_port`.
- MinIO/Temporal подключаются как внешние управляемые сервисы, параметры лежат в `config.yaml`.
- Все вызовы содержат `X-Correlation-Id` для трассировки в логах/метриках.
