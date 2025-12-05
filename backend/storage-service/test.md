# Сценарии тестирования

## Unit-тесты
- `go test ./...` — проверяет генератор ключей и работу сервиса с mock MinIO, включая `GenerateUploadURL` и перенос `x-amz-meta-correlation-id` в presigned URL.

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
6. Запросить presigned ссылку с корреляцией:
   ```bash
   grpcurl -H 'x-correlation-id: demo-corr' -d '{"document_id":"123","file_name":"demo.docx","content_length":2048,"correlation_id":"demo-corr","expires_in_seconds":600}' localhost:50052 backend.storage.v1.StorageService/GenerateUploadURL
   ```
   В ответе ожидаются `uploadUrl`, `bucket`, `objectKey` и `expiresInSeconds`; заголовок `x-amz-meta-correlation-id=demo-corr` будет частью подписанных параметров.

## Проверка приватности
- В MinIO убедитесь, что политики бакетов `knowledge-docx` и `knowledge-md` не допускают публичного доступа.
