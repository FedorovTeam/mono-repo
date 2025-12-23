# Сценарии тестирования

## Unit-тесты
- `go test ./...` — проверяет генератор ключей и работу сервиса с mock MinIO, включая `GenerateUploadURL` и перенос `x-amz-meta-correlation-id` в presigned URL.

## Ручные проверки через grpcurl
1. Собрать gRPC-сервер (после генерации protobuf-стабов).
2. Запустить MinIO с бакетами `knowledge-original` и `knowledge-md`.
3. Поднять сервис командой `go run ./cmd` — в выводе должны появиться строки об инициализации бакетов, старте gRPC-порта и обрабатке запросов с указанием `correlation_id`.
4. Выполнить загрузку оригинала:
   ```bash
   grpcurl -d '{"org_slug":"acme","project_slug":"wiki","document_id":"123","filename_extension":"docx","content":"Ym9keQ=="}' localhost:50052 backend.storage.v1.StorageService/UploadOriginal
   ```
5. Сохранить Markdown:
   ```bash
   grpcurl -d '{"org_slug":"acme","project_slug":"wiki","document_id":"123","content":"IyBIZWxsbyE="}' localhost:50052 backend.storage.v1.StorageService/UploadMarkdown
   ```
6. Получить файл:
   ```bash
   grpcurl -d '{"bucket":"knowledge-md","object_key":"org/acme/project/wiki/year=2025/month=01/day=02/123.md"}' localhost:50052 backend.storage.v1.StorageService/GetObject
   ```
7. Запросить presigned ссылку с корреляцией:
   ```bash
   grpcurl -H 'x-correlation-id: demo-corr' -d '{"document_id":"123","file_name":"demo.docx","content_length":2048,"correlation_id":"demo-corr","expires_in_seconds":600,"user_metadata":{"x-amz-meta-source":"cli-demo"}}' localhost:50052 backend.storage.v1.StorageService/GenerateUploadURL
   ```
   В ответе ожидаются `uploadUrl`, `bucket`, `objectKey`, `expiresInSeconds`, `markdownBucket`, `markdownObjectKey` и карта `requiredHeaders` (минимум `x-amz-meta-correlation-id=demo-corr` и переданные `x-amz-meta-*`), которые также попадут в `X-Amz-SignedHeaders`. Заголовки из `requiredHeaders` нужно обязательно передавать в PUT запросе, иначе MinIO вернёт ошибку подписи.
8. Выполнить загрузку по ссылке с обязательным заголовком:
   ```bash
   curl -X PUT "<uploadUrl из ответа>" \
     -H "x-amz-meta-correlation-id: demo-corr" \
     -H "x-amz-meta-source: cli-demo" \
     --data-binary @demo.docx
   ```

## Проверка приватности
- В MinIO убедитесь, что политики бакетов `knowledge-original` и `knowledge-md` не допускают публичного доступа.
