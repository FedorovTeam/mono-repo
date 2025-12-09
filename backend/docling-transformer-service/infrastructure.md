# Infrastructure view

```mermaid
graph LR
    GW[Gateway / Workflows clients] -->|gRPC :service.grpc_port (9090 по умолчанию)| TR[docling-transformer-service]
    subgraph App
        TR
    end
    TR -->|HTTP POST /v1/convert/file\nX-Correlation-Id, X-Docling-Source-Filename, X-Api-Key?\nmultipart files| DL[Docling Wrapper]
    TR -->|S3 API download/upload| MN[(MinIO buckets: original* / docling-transformations)]
    TR -->|OTLP (traces/metrics/logs)\nкогда включено| OBS[OTel Collector]
```

- gRPC сервер слушает порт из `service.grpc_port` (в `config/config.yaml` по умолчанию `9090`).
- Docling вызывается по HTTP `POST` на `/v1/convert/file` с формой `files`, прокидыванием `X-Correlation-Id`, исходного имени файла (`X-Docling-Source-Filename`) и при необходимости `X-Api-Key`.
- MinIO используется для скачивания оригинала (bucket передаётся в gRPC-запросе) и загрузки Markdown в bucket по умолчанию `docling-transformations` из настроек.
- Параметры Temporal присутствуют в конфигурации для клиента, но сервис пока не устанавливает подключение.
- Трассировка, метрики и логирование активируются флагами `observability.tracingEnabled`, `observability.metricsEnabled`, `observability.loggingEnabled`; при выключении соответствующие провайдеры и middleware не поднимаются.
