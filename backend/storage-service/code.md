# Code / Module View

```mermaid
classDiagram
    class ObjectKeyGenerator {
        -TimeProvider now
        -UUIDProvider newUUID
        +BuildKey(orgSlug, projectSlug, documentID, extension) string
    }

    class Client
    class MinioAPI {
        +BucketExists(ctx, bucket)
        +MakeBucket(ctx, bucket, opts)
        +PutObject(ctx, bucket, objectName, reader, size, opts)
        +GetObject(ctx, bucket, objectName)
        +PresignHeader(ctx, method, bucket, objectName, expires, params, headers)
        +ListenBucketNotification(ctx, bucket, prefix, suffix, events)
        +SetBucketNotification(ctx, bucket, config)
    }
    MinioAPI ..|> Client

    class MinioClientConfig {
        +string Endpoint
        +string AccessKey
        +string SecretKey
        +bool UseSSL
        +[]string Buckets
    }

    class MinioClientFactory {
        +NewMinioClient(ctx, cfg MinioClientConfig) MinioAPI
    }
    MinioClientFactory --> MinioAPI

    class Service {
        -Client client
        -ObjectKeyGenerator keyGenerator
        -string docxBucket
        -string markdownBucket
        +NewService(client, keyGenerator, docxBucket, markdownBucket) Service
        +UploadOriginal(ctx, orgSlug, projectSlug, documentID, extension, content) ObjectLocation
        +UploadMarkdown(ctx, orgSlug, projectSlug, documentID, content) ObjectLocation
        +DownloadObject(ctx, bucket, key) ObjectData
        +GenerateUploadURL(ctx, bucket, params) PresignedUpload
    }
    Service --> ObjectKeyGenerator
    Service --> Client

    class Server {
        -Client client
        -Service service
        -ObjectKeyGenerator keyGenerator
        -string uploadBucket
        -string markdownBucket
        -string orgSlug
        -string projectSlug
        +New(client, keyGenerator, uploadBucket, markdownBucket, service) Server
        +UploadOriginal(ctx, req) UploadObjectResponse
        +UploadMarkdown(ctx, req) UploadObjectResponse
        +GetObject(ctx, req) GetObjectResponse
        +GenerateUploadURL(ctx, req) GenerateUploadURLResponse
    }
    Server --> Service
    Server --> ObjectKeyGenerator
    Server --> Client

    class UploadEventHandler {
        -DocumentNotifier documentClient
        -log.Logger logger
        +handleNotification(ctx, info)
    }
    class DocumentNotifier {
        +ConfirmUpload(ctx, request)
    }
    class NotificationSetup {
        +ConfigureUploadNotification(ctx, client, bucket, targetArn) error
        +ListenAndForward(ctx, client, bucket, handler)
    }

    UploadEventHandler --> DocumentNotifier
    NotificationSetup ..> Client
    NotificationSetup ..> UploadEventHandler

    class GenerateUploadURLParams {
        +string OrgSlug
        +string ProjectSlug
        +string DocumentID
        +string FileName
        +int64 ContentLength
        +string CorrelationID
        +int64 ExpiresInSeconds
    }

    class PresignedUpload {
        +URL UploadURL
        +string Bucket
        +string ObjectKey
        +string MarkdownBucket
        +string MarkdownObjectKey
        +int64 ExpiresInSeconds
    }

    class ObjectLocation {
        +string Bucket
        +string Key
    }

    Service ..> GenerateUploadURLParams
    Service ..> PresignedUpload
    Service ..> ObjectLocation

    class ObservabilityConfig {
        +bool EnableMetrics
        +bool EnableTraces
        +bool EnableLogs
        +ConfigureProviders(cfg)
        +GRPCServerOptions(cfg, interceptors)
    }
```
