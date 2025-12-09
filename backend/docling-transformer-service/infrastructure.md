# Infrastructure view

```mermaid
graph LR
    subgraph App
        TR[docling-transformer-service]
    end
    TR -->|gRPC 9092| GW[Gateway/Workflows]
    TR -->|HTTP /convert\nX-Correlation-Id| DL[Docling Wrapper]
    TR -->|S3 API| MN[(MinIO bucket docling-transformations)]
    TR -->|Client| TMP[Temporal Cloud]
    TR -->|Logs/Tracing/metrics\nwhen enabled| OBS[OTel Collector]
```

- gRPC порт читается из `service.grpc_port`.
- MinIO/Temporal подключаются как внешние управляемые сервисы, параметры лежат в `config.yaml`.
- Все вызовы содержат `X-Correlation-Id` для трассировки в логах/метриках.
- Трассировка, метрики и логирование активируются флагами `observability.tracingEnabled`, `observability.metricsEnabled`, `observability.loggingEnabled`; при выключении соответствующие провайдеры и middleware не поднимаются.
