# test.md

## Требуемые сервисы из `infra`
- Temporal (namespace `knowledge`, task queue `knowledge-documents`).
- MinIO (bucket для исходников и markdown, используется document-service).
- PostgreSQL (хранение статусов в backend-document-service).

## Готовые переменные
```
API_KEY=secret
HOST=http://localhost:8080/query
DOC_ID=<заполнить после загрузки>
```

## Запросы GraphQL (curl)
### Загрузка документа
```
curl -X POST "$HOST" \
  -H "Content-Type: application/json" \
  -H "X-API-Key: $API_KEY" \
  -H "X-Correlation-ID: demo-corr-id" \
  -d '{
    "query": "mutation Upload($input: UploadKnowledgeDocumentInput!){ uploadKnowledgeDocument(input:$input){ id status } }",
    "variables": {
      "input": {
        "fileName": "demo.docx",
        "contentBase64": "ZHVtbXk=",
        "repository": "org/repo",
        "branch": "main",
        "path": "docs/demo.md"
      }
    }
  }'
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

