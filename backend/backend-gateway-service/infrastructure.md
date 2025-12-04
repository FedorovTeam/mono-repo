# infrastructure

```mermaid
flowchart LR
    subgraph Kubernetes
      Gateway[backend-gateway-service] -->|gRPC 50051| DocumentService
      Gateway -->|Temporal SDK| Temporal[Temporal Frontend]
    end

    DocumentService -->|PostgreSQL| DB[(Postgres)]
    DocumentService -->|S3 API| MinIO[(MinIO)]
```

