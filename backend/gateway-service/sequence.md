# sequence

```mermaid
sequenceDiagram
    participant Client
    participant Gateway
    participant StorageService
    participant MinIO
    participant DocumentService

    Client->>Gateway: GraphQL mutation uploadKnowledgeDocument (X-API-Key, optional X-Correlation-ID)
    Gateway->>Gateway: CorrelationIDMiddleware сохраняет/генерирует correlation-id и проставляет его в ответ
    Gateway->>Gateway: APIKeyMiddleware проверяет X-API-Key (кроме OPTIONS)
    Gateway->>Gateway: validateUploadInput проверяет fileName, repository[/owner], branch, path, fileSizeBytes, extension по allowlist/лимиту
    Gateway->>Gateway: сгенерировать documentId (UUID)
    Gateway->>StorageService: GenerateUploadURL(DocumentId, FileName, ContentLength, CorrelationId, ExpiresInSeconds, gRPC meta: X-Correlation-ID + X-API-Key)
    StorageService-->>Gateway: uploadUrl + bucket + objectKey
    Gateway->>DocumentService: CreateDocument(id, fileName, repository, [repositoryOwner], branch, path, contentLength, upload bucket/objectKey, correlation-id; gRPC meta: X-Correlation-ID + X-API-Key)
    DocumentService-->>Gateway: документ (id/status)
    Gateway-->>Client: uploadUrl + uploadMethod=PUT + document{id,status}
    Client-->>MinIO: PUT файла по presigned URL
    MinIO-->>Gateway: POST /internal/storage/uploaded (s3:ObjectCreated:Put, correlation-id в userMetadata/responseElements)
    Gateway->>Gateway: извлечь documentId из objectKey (basename до расширения); correlation-id из вебхука или новый
    Gateway->>DocumentService: ConfirmUpload(document_id, bucket, object_key, correlation-id; gRPC meta: X-Correlation-ID)
    DocumentService-->>Gateway: подтверждение; Gateway отвечает HTTP 202, иначе 400 если ни одна запись не обработана
```

