# contracts

## GraphQL
- `mutation uploadKnowledgeDocument(input: UploadKnowledgeDocumentInput!): KnowledgeDocument!`
- `mutation retryProcessing(documentId: ID!): KnowledgeDocument!`
- `query knowledgeDocument(id: ID!): KnowledgeDocument`
- `query knowledgeDocuments(filter: KnowledgeDocumentFilter, pagination: Pagination): KnowledgeDocumentConnection!`

Схема лежит в `graph/schema.graphqls`, код генерируется `github.com/99designs/gqlgen`.

## gRPC backend-document-service
Копия контракта размещена в `proto/backend/document/v1/document.proto`.

Минимальный набор методов:
- `CreateDocument` — регистрация документа и ключей в MinIO.
- `GetDocument` — получение состояния по ID.
- `ListDocuments` — пагинированный список.
- `RetryProcessing` — повторный запуск обработки.

Все вызовы получают `x-correlation-id` в metadata.

## Temporal
Запускается workflow `KnowledgeDocumentWorkflow` с входными данными файла (ключи в MinIO, GitHub-путь). Task queue задаётся в конфигурации, correlation-id пробрасывается в start options и логи.

