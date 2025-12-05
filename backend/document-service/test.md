# Тестирование

## Автоматические тесты
- `go test ./...`

## Ручные проверки через grpcurl
1. Создать документ:
   ```bash
   grpcurl -plaintext -d '{"name":"demo.docx","original_bucket":"backend-original","markdown_bucket":"backend-md"}' localhost:50051 backend.document.v1.DocumentService/CreateDocument
   ```
2. Получить документ:
   ```bash
   grpcurl -plaintext -d '{"id":"<uuid>"}' localhost:50051 backend.document.v1.DocumentService/GetDocument
   ```
3. Обновить статус:
   ```bash
   grpcurl -plaintext -d '{"id":"<uuid>","status":"DOCUMENT_STATUS_STORED_ORIGINAL","version":2,"markdown_object_key":"documents/<uuid>/markdown"}' localhost:50051 backend.document.v1.DocumentService/UpdateDocumentStatus
   ```
4. Список документов:
   ```bash
   grpcurl -plaintext -d '{"limit":10}' localhost:50051 backend.document.v1.DocumentService/ListDocuments
   ```

Перед ручными тестами примените миграции и запустите сервер: `go run ./cmd/server --migrate && go run ./cmd/server`.
