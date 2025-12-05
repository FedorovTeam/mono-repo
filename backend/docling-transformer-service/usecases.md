# Use cases

## Конвертация загруженного документа
- **Акторы:** Gateway-service или Temporal Activity.
- **Шаги:**
  1. Получить байты DOCX из MinIO/загрузки.
  2. Вызвать gRPC `ConvertDocxToMarkdown` с `file_name`, `docx_content`, `content_type` и `x-correlation-id`.
  3. Сохранить Markdown в MinIO (bucket `docling-transformations`).
  4. Передать результат дальше в workflow публикации в GitHub.

## Повторная обработка с новым correlation-id
- **Цель:** привязать ретраи к новому trace.
- **Шаги:**
  1. Сгенерировать новый `x-correlation-id` перед gRPC вызовом.
  2. Повторить шаги конвертации; логи сервиса будут отделены от предыдущего запуска.

## Локальная отладка Pandoc
- **Акторы:** разработчик.
- **Шаги:**
  1. Запустить простую заглушку Pandoc (`python -m http.server` с хендлером POST `/convert`).
  2. Указать её в `pandoc.endpoint` и выполнить gRPC вызов.
  3. Проверить, что в заглушку прилетает корректный заголовок `X-Correlation-Id` и тело DOCX.
