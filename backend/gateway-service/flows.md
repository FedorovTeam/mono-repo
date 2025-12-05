# flows

## Upload → Convert → Publish
1. Клиент отправляет `uploadKnowledgeDocument` с метаданными файла и контента.
2. gateway проверяет `X-API-Key`, генерирует/пробрасывает `correlation-id` и создаёт запись через gRPC `CreateDocument` в backend-document-service.
3. Через Temporal запускается workflow `KnowledgeDocumentWorkflow` с шагами upload → convert → publish.
4. document-service обновляет статусы, gateway отдаёт ID и текущий статус.
5. При ошибке клиент может вызвать `retryProcessing`, что инициирует повторное выполнение workflow для нужного документа.

## Получение статуса
1. Запрос `knowledgeDocument` или `knowledgeDocuments` направляется в backend-document-service.
2. correlation-id добавляется в gRPC metadata и логи.
3. Ответы мапятся в GraphQL типы и возвращаются клиенту.

