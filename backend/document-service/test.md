# Тестирование

## Автоматические тесты
- `go test ./...`

## Наблюдаемость
- По умолчанию экспорт метрик и трейсинга выключен, а логи пишутся в stdout. Чтобы проверить интеграцию с OTLP Collector, в `config/config.yaml` установите `observability.traces.enabled` и/или `observability.metrics.enabled` в `true` и задайте переменную `OTEL_EXPORTER_OTLP_ENDPOINT` (например, `otel-collector:4317`).
- Если хотите отключить любые записи в stdout (например, при нагрузочных тестах), установите `observability.logs.enabled: false` — логгер перейдёт в no-op режим, но фатальные ошибки по-прежнему завершают процесс.

## Ручные проверки через grpcurl
1. Создать документ:
   ```bash
   grpcurl -plaintext -d '{"name":"demo.docx","original_bucket":"knowledge-original","markdown_bucket":"knowledge-md"}' localhost:50051 backend.document.v1.DocumentService/CreateDocument
   ```
2. Получить документ:
   ```bash
   grpcurl -plaintext -d '{"id":"<uuid>"}' localhost:50051 backend.document.v1.DocumentService/GetDocument
   ```
3. Обновить статус:
   ```bash
   grpcurl -plaintext -d '{"id":"<uuid>","status":"DOCUMENT_STATUS_STORED_ORIGINAL","version":2,"markdown_object_key":"documents/<uuid>/<uuid>.md"}' localhost:50051 backend.document.v1.DocumentService/UpdateDocumentStatus
   ```
4. Список документов:
   ```bash
   grpcurl -plaintext -d '{"limit":10}' localhost:50051 backend.document.v1.DocumentService/ListDocuments
   ```

Перед ручными тестами примените миграции и запустите сервер: `go run ./cmd/server --migrate && go run ./cmd/server`.
