# Diagrams

```mermaid
graph LR
  DS[Document Service publish worker] -->|PublishMarkdown\n(idempotency_key, retry_policy)| Publisher
  Publisher[Github Publisher Service] -->|load markdown\n(bucket/object_key)| MinIO[(MinIO)]
  Publisher -->|commit content| GitHub[(GitHub API)]
  Publisher -->|open PR| GitHub
  Publisher -->|idempotency cache| Cache{{In-memory cache}}
  Publisher -->|metrics/traces/logs (OTLP)| OTLP[(Observability Collector)]
  GitHub -->|retry_after, commit_sha, pr links| DS
```
