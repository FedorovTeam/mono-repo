# Use cases

## Конвертация документа из MinIO в Markdown
- **Акторы:** Gateway-service или Temporal Activity.
- **Валидация:** `document_id` обязателен; `original_bucket` и `original_object_key` обязательны; `markdown_bucket` берётся из запроса или из конфигурации, иначе возвращается `InvalidArgument`.
- **Шаги:**
  1. Принять gRPC вызов `ConvertDocxToMarkdown` с `document_id`, `original_bucket`, `original_object_key`, необязательными `markdown_bucket`, `markdown_object_key` и `x-correlation-id` в метаданных.
  2. Скачать исходный DOCX из MinIO (повторные попытки при `NoSuchKey`).
  3. Определить имя файла из `original_object_key` и сделать до трёх HTTP POST запросов в Docling на `<docling.endpoint>/v1/convert/file`, прокидывая заголовки `X-Correlation-Id`, `X-Docling-Source-Filename`, `X-Docling-Endpoint`, `X-Api-Key` (если настроен) и multipart тело `files` c исходным документом и корректным `Content-Type` (по умолчанию `application/vnd.openxmlformats-officedocument.wordprocessingml.document`).
  4. Принять Markdown из JSON `document.md_content` при статусах `success/partial_success`; если Docling вернул другой Content-Type, использовать тело ответа и заголовок.
  5. Определить `markdown_object_key`: взять из запроса или построить из `original_object_key`, заменив расширение на `.md`.
  6. Сохранить Markdown в MinIO в бакет `markdown_bucket` с медиа-типом из Docling (по умолчанию `text/markdown`).
  7. Вернуть вызывающей стороне `markdown_content`, `media_type`, фактические `markdown_bucket` и `markdown_object_key`.

## Повторная обработка с новым correlation-id
- **Цель:** привязать ретраи к новому trace и логам.
- **Шаги:**
  1. Сгенерировать или передать новый `x-correlation-id` в метаданных gRPC перед повторным вызовом.
  2. Повторить шаги конвертации; interceptors сервиса и клиент Docling прокинут новый correlation-id в логи и заголовки.

## Локальная отладка Docling
- **Акторы:** разработчик.
- **Шаги:**
  1. Запустить заглушку Docling с POST хендлером на `/v1/convert/file`, который принимает `multipart/form-data` с `files` и отдаёт JSON с `document.md_content`.
  2. Указать URL заглушки в `docling.endpoint` и выполнить gRPC вызов `ConvertDocxToMarkdown`.
  3. Проверить, что заглушка получила заголовки `X-Correlation-Id`, `X-Docling-Source-Filename`, `X-Docling-Endpoint`, `X-Api-Key` (при наличии) и тело исходного документа внутри multipart.
