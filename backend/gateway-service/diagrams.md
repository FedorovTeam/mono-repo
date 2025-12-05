# diagrams

```mermaid
flowchart LR
    Client -->|GraphQL| Gateway
    Gateway -->|gRPC| DocumentService
    Gateway -->|Temporal| Temporal
    DocumentService -->|MinIO| Storage
    DocumentService -->|Postgres| DB
```

