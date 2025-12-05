# Диаграммы

## Компонентная схема
```mermaid
graph TD
    Client[Gateway/Activity] -->|gRPC| API[TransformerService]
    API --> INT[LoggingInterceptor]
    API --> CONV[DoclingConverter]
    CONV --> DOC[Docling HTTP Stub]
    CONV -. headers .-> DOC
```

## Логическая связка с хранилищем
```mermaid
graph LR
    DOCX[DOCX input] --> API
    API --> MD[Markdown output]
    MD --> MINIO[(MinIO bucket docling-transformations)]
    MD --> GH[GitHub Publisher (downstream)]
```
