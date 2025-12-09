# Тестирование

## Автотесты
- `go test ./...` — unit тесты конвертера и gRPC-обвязки, e2e против заглушки Docling (bufconn + httptest).

## Ручные проверки
### gRPC через grpcurl
```bash
grpcurl -plaintext -d '{"documentId":"doc-1","originalBucket":"orig","originalObjectKey":"file.docx","markdownBucket":"md","filenameExtension":"docx"}' localhost:9092 backend.transformer.v1.TransformerService/ConvertDocumentToMarkdown
```
Ожидаемый ответ — JSON c полем `markdown`, где лежит строка из Docling, а в логах сервера присутствует переданный/сгенерированный `correlation_id`.

### Проверка логов
1. Запустить сервер с `CONFIG_PATH`.
2. Выполнить gRPC запрос с заголовком `x-correlation-id: manual-check-1`.
3. Убедиться, что в логах есть строчки `incoming gRPC request` и `ConvertDocumentToMarkdown completed` с тем же идентификатором и что прокинуты заголовки до Docling.

### Интеграция с Docling-заглушкой
1. Поднять локальный HTTP-сервис, отвечающий на POST `/v1/convert/file`, принимающий multipart поле `files` и возвращающий JSON `{"document":{"md_content":"..."},"status":"success"}`.
2. Указать его адрес в `docling.endpoint` и при необходимости `docling.api_key`.
3. Вызвать gRPC метод — тело исходного документа должно дойти до заглушки внутри multipart, а в ответе gRPC вернётся Markdown из неё.
