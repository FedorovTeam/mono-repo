# Инфраструктура

```mermaid
deploymentDiagram
  node "k8s namespace" {
    node "storage-service" {
      component "gRPC" as grpc
      component "MinIO client" as minioClient
    }
    node "minio" {
      database "knowledge-original" as bucketDocx
      database "knowledge-md" as bucketMd
    }
    node "gateway" {
      component "Webhook (SQS)" as gatewayWebhook
    }
  }

  grpc --> minioClient
  minioClient --> bucketDocx
  minioClient --> bucketMd
  bucketDocx --> gatewayWebhook : s3:ObjectCreated
```

- Buckets `knowledge-original` и `knowledge-md` остаются приватными.
- Доступ осуществляется по `minio.endpoint` с ключами из `config.yaml`.
- При старте сервис создаёт недостающие бакеты из `minio.buckets` и `events.upload_bucket`.
- Для бакета загрузки (`events.upload_bucket`, по умолчанию `knowledge-original`) включается webhook с ARN `events.webhook_arn`, чтобы
  отправлять уведомления `s3:ObjectCreated` в gateway.
- RPC `GenerateUploadURL` подписывает PUT-запрос в бакет `knowledge-original`, используя ключ формата `org/<org_slug>/project/<project_slug>/year=YYYY/month=MM/day=DD/<document_id>.<ext>` и фиксируя `x-amz-meta-correlation-id` из входящего запроса.
