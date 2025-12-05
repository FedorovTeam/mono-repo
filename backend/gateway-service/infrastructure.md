# infrastructure

```mermaid
flowchart LR
    subgraph Kubernetes
      Gateway[backend-gateway-service] -->|gRPC 50051| DocumentService
      Gateway -->|gRPC 50052| StorageService
      Gateway -->|Temporal SDK| Temporal[Temporal Frontend]
      Gateway -->|OTLP 4317| OTelCollector[(OTLP Collector)]
    end

    DocumentService -->|PostgreSQL| DB[(Postgres)]
    StorageService -->|S3 API| MinIO[(MinIO)]
```

Gateway дергает `StorageService.GenerateUploadURL` по gRPC (порт 50052), чтобы получить `uploadUrl`, `bucket` и `objectKey` для прямой загрузки в MinIO. TTL presigned ссылки задаётся `uploads.signedUrlTtlSeconds`, correlation-id сохраняется как `x-amz-meta-correlation-id`.

