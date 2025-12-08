# sequence

```mermaid
sequenceDiagram
    participant Client
    participant Gateway
    participant StorageService
    participant DocumentService
    participant Temporal

    Client->>Gateway: uploadKnowledgeDocument (X-API-Key + optional X-Correlation-ID)
    Gateway->>Gateway: обеспечить/создать correlation-id и проставить X-Correlation-ID
    Gateway->>Gateway: проверить X-API-Key
    Gateway->>Gateway: validate upload input (fileName/repository/branch/path/size/ext)
    Gateway->>StorageService: GenerateUploadURL (gRPC + correlation-id)
    StorageService-->>Gateway: uploadUrl + bucket/objectKey
    Gateway->>DocumentService: CreateDocument (metadata + size + upload bucket/objectKey + correlation-id)
    DocumentService-->>Gateway: документ (id/status[/workflowId])
    Gateway->>Temporal: StartUploadWorkflow (upload url + bucket/objectKey + metadata + correlation-id)
    Temporal-->>Gateway: WorkflowID
    Gateway-->>Client: uploadUrl + uploadMethod=PUT + document{id,status,workflowId}
    Client-->>StorageService: PUT DOCX по signed URL
```

