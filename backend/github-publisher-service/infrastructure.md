# Infrastructure

```mermaid
graph TD
  subgraph Runtime
    A[gRPC Github Publisher Service]
  end
  subgraph External
    B[GitHub API]
    C[MinIO]
    D[OTLP Collector]
  end
  subgraph Config
    E[config.yaml]
  end
  subgraph Clients
    F[[gRPC Clients (document-service stage worker)]]
  end

  F -->|PublishMarkdown| A
  A -- token, repo, author, minio, otlp --> E
  A -- HTTPS REST --> B
  A -- S3 GetObject --> C
  A -- OTLP gRPC --> D
```
