# Диаграммы

## Компонентная схема
```mermaid
graph TD
    Client[Gateway/Activity] -->|gRPC| API[TransformerService]
    API --> INT[LoggingInterceptor]
    API --> CONV[PandocConverter]
    CONV --> PANDOC[Pandoc HTTP Stub]
    CONV -. headers .-> DOC[Docling endpoint]
```

## Логическая связка с хранилищем
```mermaid
graph LR
    DOCX[DOCX input] --> API
    API --> MD[Markdown output]
    MD --> MINIO[(MinIO bucket docling-transformations)]
    MD --> GH[GitHub Publisher (downstream)]
```
