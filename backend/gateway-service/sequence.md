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
    Gateway->>Gateway: APIKeyMiddleware проверяет X-API-Key (кроме OPTIONS) и сохраняет его в контексте
    Gateway->>Gateway: validateUploadInput проверяет fileName, repository[/owner], branch, path, fileSizeBytes, extension по allowlist/лимиту и нормализует repository/owner
    Gateway->>Gateway: сгенерировать documentId (UUID)
    Gateway->>StorageService: GenerateUploadURL(DocumentId, FileName, ContentLength, CorrelationId, ExpiresInSeconds, gRPC meta: X-Correlation-ID + X-API-Key)
    StorageService-->>Gateway: uploadUrl + upload bucket/objectKey + markdown bucket/objectKey
    Gateway->>DocumentService: CreateDocument(id, fileName, repository, [repositoryOwner], branch, path, contentLength, upload bucket/objectKey, markdown bucket/objectKey, correlation-id; gRPC meta: X-Correlation-ID + X-API-Key)
    DocumentService-->>Gateway: документ (id/status/jobId)
    Gateway-->>Client: uploadUrl + uploadMethod=PUT + документ{id,status,jobId}
    Client-->>MinIO: PUT файла по presigned URL
    MinIO-->>Gateway: POST /internal/storage/uploaded (s3:ObjectCreated:Put, correlation-id в userMetadata/responseElements)
    loop records in webhook
        Gateway->>Gateway: декодировать objectKey (PathUnescape), извлечь documentId из basename без расширения
        Gateway->>Gateway: correlation-id из userMetadata/responseElements (case-insensitive) или новый через EnsureCorrelationID
        Gateway->>DocumentService: ConfirmUpload(document_id, bucket, object_key, correlation-id; gRPC meta: X-Correlation-ID)
        DocumentService-->>Gateway: подтверждение
    end
    Gateway-->>MinIO: HTTP 202 если обработаны записи, иначе 400
```

