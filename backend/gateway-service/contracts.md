# contracts

## GraphQL
- `mutation uploadKnowledgeDocument(input: UploadKnowledgeDocumentInput!): UploadKnowledgeDocumentPayload!`
- `query knowledgeDocument(id: ID!): KnowledgeDocument`
- `query knowledgeDocuments(filter: KnowledgeDocumentFilter, pagination: Pagination): KnowledgeDocumentConnection!`

Схема лежит в `graph/schema.graphqls`, код генерируется `github.com/99designs/gqlgen`.

Все запросы и мутации требуют заголовок `Authorization: Bearer <jwt>`; при `security.apiKey.enabled=true` допускается `X-API-Key`. Необязательный `X-Correlation-ID` пробрасывается в gRPC метаданные клиентов вместе с `x-user-id`/`x-user-email`/`x-user-groups`.

## gRPC backend-document-service
Копия контракта размещена в `proto/backend/document/v1/document.proto`.

Минимальный набор методов:
- `CreateDocument` — регистрация документа и ключей в MinIO.
- `GetDocument` — получение состояния по ID.
- `ListDocuments` — пагинированный список.
- `ConfirmUpload` — подтверждение факта загрузки объекта в хранилище.

Все вызовы получают `x-correlation-id` в metadata.
