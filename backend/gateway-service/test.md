# test.md

## Требуемые сервисы из `infra`
- Temporal (namespace `knowledge`, task queue `knowledge-documents`).
- MinIO (bucket для исходников и markdown, используется storage-service).
- PostgreSQL (хранение статусов в backend-document-service).
- storage-service (gRPC endpoint для выдачи signed URL).
- OTLP collector (принимает метрики/трейсинг от gateway).

## Готовые переменные
```
API_KEY=secret
HOST=http://localhost:8080/query
DOC_ID=<заполнить после загрузки>
```

## Запросы GraphQL (curl)
### Загрузка документа и получение signed URL
```
curl -X POST "$HOST" \
  -H "Content-Type: application/json" \
  -H "X-API-Key: $API_KEY" \
  -H "X-Correlation-ID: demo-corr-id" \
  -d '{
    "query": "mutation Upload($input: UploadKnowledgeDocumentInput!){ uploadKnowledgeDocument(input:$input){ uploadUrl uploadMethod document { id status workflowId } } }",
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

### Повторная обработка
```
curl -X POST "$HOST" \
  -H "Content-Type: application/json" \
  -H "X-API-Key: $API_KEY" \
  -d '{"query": "mutation { retryProcessing(documentId: \"'$DOC_ID'\"){ id status } }"}'
```

Все вызовы возвращают заголовок `X-Correlation-ID`, который прокидывается в gRPC и Temporal и попадает в OTLP трейсы.

