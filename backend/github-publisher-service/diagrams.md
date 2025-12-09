# Diagrams

```mermaid
graph LR
  Client[[gRPC caller]] -->|PublishMarkdown| Publisher
  Publisher[Github Publisher Service] -->|commit content| GitHub[(GitHub API)]
  Publisher -->|open PR| GitHub
  Publisher -->|metrics/traces/logs (OTLP)| OTLP[(Observability Collector)]
```
