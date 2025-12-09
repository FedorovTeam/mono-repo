# code

```mermaid
classDiagram
    class Server {
        +Run(ctx)
        +handleStorageNotification(w, r)
    }
    class GraphQLResolver {
        +UploadKnowledgeDocument()
        +RetryProcessing()
        +KnowledgeDocument()
        +KnowledgeDocuments()
    }
    class UploadValidationConfig {
        MaxSizeBytes
        AllowedExtensions
        SignedURLTTLSeconds
    }
    class DocumentClient {
        +CreateDocument()
        +GetDocument()
        +ListDocuments()
        +RetryProcessing()
        +ConfirmUpload()
        +Close()
    }
    class StorageClient {
        +GenerateUploadURL()
        +Close()
    }
    class Middleware {
        +APIKeyMiddleware()
        +CorrelationIDMiddleware()
        +CORSMiddleware()
    }
    class Observability {
        +HTTPHandler()
        +GRPCDialOptions()
    }
    Server --> GraphQLResolver
    Server --> Middleware
    Server --> Observability
    GraphQLResolver --> DocumentClient
    GraphQLResolver --> StorageClient
    Server --> DocumentClient : ConfirmUpload (webhook)
    GraphQLResolver --> UploadValidationConfig
```

