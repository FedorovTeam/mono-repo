# Тестирование

## Автотесты
- `go test ./...` — unit тесты конвертера и gRPC-обвязки, e2e против заглушки Docling (bufconn + httptest).

## Ручные проверки
### gRPC через grpcurl
```bash
grpcurl -plaintext -d '{"docxContent":"ZmlsZSBiYXRlcyA="}' localhost:9092 backend.transformer.v1.TransformerService/ConvertDocxToMarkdown
```
Ожидаемый ответ — JSON c полем `markdown`, где лежит строка из Docling, а в логах сервера присутствует переданный/сгенерированный `correlation_id`.

### Проверка логов
1. Запустить сервер с `CONFIG_PATH`.
2. Выполнить gRPC запрос с заголовком `x-correlation-id: manual-check-1`.
3. Убедиться, что в логах есть строчки `incoming gRPC request` и `ConvertDocxToMarkdown completed` с тем же идентификатором и что прокинуты заголовки до Docling.

### Интеграция с Docling-заглушкой
1. Поднять локальный HTTP-сервис, отвечающий на POST `/convert` статической строкой Markdown.
2. Указать его адрес в `pandoc.endpoint` (Docling-конвертер) и при необходимости добавить `docling.endpoint` для трассировки.
3. Вызвать gRPC метод — тело запроса должно дойти до заглушки, а в ответе gRPC вернётся Markdown из неё.
