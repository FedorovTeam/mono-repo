# Сценарии тестирования

## Unit-тесты
- `go test ./...` — проверяет генератор ключей и работу сервиса с mock MinIO.

## Ручные проверки через grpcurl
1. Собрать gRPC-сервер (после генерации protobuf-стабов).
2. Запустить MinIO с бакетами `knowledge-docx` и `knowledge-md`.
3. Выполнить загрузку оригинала:
   ```bash
   grpcurl -d '{"org_slug":"acme","project_slug":"wiki","document_id":"123","filename_extension":"docx","content":"Ym9keQ=="}' localhost:50052 backend.storage.v1.StorageService/UploadOriginal
   ```
4. Сохранить Markdown:
   ```bash
   grpcurl -d '{"org_slug":"acme","project_slug":"wiki","document_id":"123","content":"IyBIZWxsbyE="}' localhost:50052 backend.storage.v1.StorageService/UploadMarkdown
   ```
5. Получить файл:
   ```bash
   grpcurl -d '{"bucket":"knowledge-md","object_key":"org/acme/project/wiki/year=2025/month=01/day=02/123.md"}' localhost:50052 backend.storage.v1.StorageService/GetObject
   ```

## Проверка приватности
- В MinIO убедитесь, что политики бакетов `knowledge-docx` и `knowledge-md` не допускают публичного доступа.
