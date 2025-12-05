# Use cases

## Конвертация загруженного документа
- **Акторы:** Gateway-service или Temporal Activity.
- **Шаги:**
  1. Получить байты DOCX из MinIO/загрузки.
  2. Вызвать gRPC `ConvertDocxToMarkdown` с `file_name`, `docx_content`, `content_type` и `x-correlation-id`.
  3. Docling-конвертер возвращает Markdown; сервис отдаёт его вызывающей стороне.
  4. Сохранить Markdown в MinIO (bucket `docling-transformations`).
  5. Передать результат дальше в workflow публикации в GitHub.

## Повторная обработка с новым correlation-id
- **Цель:** привязать ретраи к новому trace.
- **Шаги:**
  1. Сгенерировать новый `x-correlation-id` перед gRPC вызовом.
  2. Повторить шаги конвертации; логи сервиса и Docling будут отделены от предыдущего запуска.

## Локальная отладка Docling
- **Акторы:** разработчик.
- **Шаги:**
  1. Запустить простую заглушку Docling (хендлер POST `/convert`, возвращающий заранее подготовленный Markdown).
  2. Указать её в `docling.endpoint` и выполнить gRPC вызов.
  3. Проверить, что в заглушку прилетает корректный заголовок `X-Correlation-Id`, `X-Source-Filename` и тело DOCX.
