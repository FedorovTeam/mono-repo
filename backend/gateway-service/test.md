# test.md

## Требуемые сервисы из `infra`
- storage-service (gRPC endpoint для выдачи signed URL; сам использует MinIO).
- backend-document-service (gRPC API состояний документов).
- OTLP collector (принимает метрики/трейсинг от gateway, опционально — по `observability.enabled`).

## Готовые переменные
```
API_KEY=secret
HOST=http://localhost:8080/query
DOC_ID=<заполнить после загрузки>
```

Все GraphQL запросы и мутации требуют заголовок `X-API-Key`. Необязательный `X-Correlation-ID` будет возвращён в ответе и попадёт в gRPC-метаданные downstream-сервисов.

## Запросы GraphQL (curl)
### Загрузка документа и получение signed URL
```
curl -X POST "$HOST" \
  -H "Content-Type: application/json" \
  -H "X-API-Key: $API_KEY" \
  -H "X-Correlation-ID: demo-corr-id" \
  -d '{
    "query": "mutation Upload($input: UploadKnowledgeDocumentInput!){ uploadKnowledgeDocument(input:$input){ uploadUrl uploadMethod document { id status jobId } } }",
    "variables": {
      "input": {
        "fileName": "demo.docx",
        "fileSizeBytes": 2048,
        "repository": "org/repo",
        "branch": "main",
        "path": "docs/demo.md"
      }
    }
  }'
```

Ответ содержит `uploadUrl` и HTTP метод (`PUT`). Далее загрузите файл напрямую в MinIO:

```
curl -X PUT "<uploadUrl из ответа>" \
  -H "Content-Type: application/vnd.openxmlformats-officedocument.wordprocessingml.document" \
  --data-binary @./demo.docx
```

storage-service возвращает `bucket`, `objectKey` и `expiresInSeconds`, которые gateway прокидывает в `document-service.CreateDocument`. Заголовок `X-Correlation-ID` автоматически попадает в presigned URL через метаданные `x-amz-meta-correlation-id`.

### Получение одного документа
```
curl -X POST "$HOST" \
  -H "Content-Type: application/json" \
  -H "X-API-Key: $API_KEY" \
  -d '{"query": "{ knowledgeDocument(id: \"'$DOC_ID'\"){ id status } }"}'
```

### Список документов
```
curl -X POST "$HOST" \
  -H "Content-Type: application/json" \
  -H "X-API-Key: $API_KEY" \
  -d '{"query": "{ knowledgeDocuments(pagination:{limit:10,offset:0}){ nodes { id status fileName } totalCount } }"}'
```

Все вызовы возвращают заголовок `X-Correlation-ID`, который прокидывается в gRPC и попадает в OTLP трейсы.

### Восстановление после пропущенного webhook MinIO
Если объект уже лежит в bucket, но статус документа остаётся `Uploaded` (webhook не дошёл до gateway), можно повторно отправить уведомление вручную — обработчик `/internal/storage/uploaded` идемпотентен и спокойно примет дубликат:

```
curl -X POST "http://localhost:8080/internal/storage/uploaded" \
  -H "Content-Type: application/json" \
  -d '{
    "Records": [{
      "s3": {
        "bucket": {"name": "knowledge-original"},
        "object": {
          "key": "<objectKey из storage-service>",
          "userMetadata": {"x-amz-meta-correlation-id": "manual-retry"}
        }
      }
    }]
  }'
```

