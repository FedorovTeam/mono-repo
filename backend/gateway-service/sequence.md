# sequence

```mermaid
sequenceDiagram
    participant Client
    participant Gateway
    participant StorageService
    participant MinIO
    participant DocumentService
    participant Temporal

    Client->>Gateway: uploadKnowledgeDocument (X-API-Key + optional X-Correlation-ID)
    Gateway->>Gateway: обеспечить/создать correlation-id и проставить X-Correlation-ID
    Gateway->>Gateway: проверить X-API-Key
    Gateway->>Gateway: validate upload input (fileName/repository/branch/path/size/ext)
    Gateway->>Gateway: сгенерировать documentId (UUID)
    Gateway->>StorageService: GenerateUploadURL (DocumentId + size + TTL, gRPC + correlation-id + X-API-Key)
    StorageService-->>Gateway: uploadUrl + bucket/objectKey
    Gateway->>DocumentService: CreateDocument (documentId + metadata + size + upload bucket/objectKey + correlation-id + X-API-Key)
    DocumentService-->>Gateway: документ (id/status[/workflowId])
    Gateway-->>Client: uploadUrl + uploadMethod=PUT + document{id,status,workflowId}
    Client-->>MinIO: PUT DOCX по signed URL
    MinIO-->>Gateway: webhook s3:ObjectCreated:Put (correlation-id в userMetadata/responseElements)
    Gateway->>Gateway: извлечь documentId из objectKey; использовать correlation-id из вебхука или сгенерировать
    Gateway->>DocumentService: ConfirmUpload(document_id, bucket, object_key, correlation-id + X-API-Key)
    DocumentService->>Temporal: старт Workflow
```

