# Инфраструктура

```mermaid
deploymentDiagram
  node "k8s namespace" {
    node "storage-service" {
      component "gRPC" as grpc
      component "MinIO client" as minioClient
    }
    node "minio" {
      database "knowledge-docx" as bucketDocx
      database "knowledge-md" as bucketMd
    }
  }

  grpc --> minioClient
  minioClient --> bucketDocx
  minioClient --> bucketMd
```

- Buckets `knowledge-docx` и `knowledge-md` остаются приватными.
- Доступ осуществляется по `minio.endpoint` с ключами из `config.yaml`.
