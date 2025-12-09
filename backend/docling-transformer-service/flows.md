# Flows

## Конвертация DOCX → Markdown
1. Gateway/Temporal вызывает gRPC метод `ConvertDocumentToMarkdown` с `document_id`, `original_bucket`, `original_object_key`, `filename_extension` и `x-correlation-id` (опционально `markdown_bucket`/`markdown_object_key`).
2. Интерсептор (включается флагом `observability.loggingEnabled`) сохраняет/генерирует correlation-id и пишет входные метаданные в лог.
3. Сервис проверяет наличие `document_id`, `original_bucket` и `original_object_key`, валидирует расширение (`doc`, `docx`, `pdf`); если `markdown_bucket` не указан, используется значение из `config.yaml`.
4. MinIO-адаптер скачивает оригинал с повторными попытками при ошибке `NoSuchKey` (до 10 попыток с увеличивающейся паузой).
5. `DoclingConverter` отправляет POST `${docling.endpoint}/v1/convert/file` (до 3 попыток). В запрос передаются `X-Correlation-Id`, `X-Docling-Source-Filename` (имя берётся из `original_object_key` или подставляется `document.docx`), `X-Api-Key` (если задан в конфиге), `X-Docling-Endpoint`, а тело — `multipart/form-data` с полем `files` (один файл с `Content-Type` исходника).
6. Ответ Docling обрабатывается как Markdown: при `Content-Type: application/json` читается `document.md_content` со статусом `success/partial_success`, в остальных случаях берётся тело ответа и `Content-Type` из заголовка (если пусто — `text/markdown`).
7. Markdown сохраняется в MinIO в `markdown_bucket` с ключом из запроса или вычисленным по исходному пути (`<original>.md`); в `PutObject` передаётся итоговый media type.
8. gRPC-слой отдаёт `markdown_content`, `media_type`, `markdown_bucket` и `markdown_object_key` вызывающей стороне; correlation-id можно использовать для увязки со следующими шагами (публикация в GitHub/Temporal и т.д.).

## Управление конфигурацией
1. При старте `cmd/server` читает `config.yaml` (или путь из `CONFIG_PATH`).
2. Все интеграции (Docling, MinIO, Temporal) берут параметры из конфига, без `.env`.
3. Любые изменения конфига требуют перезапуска сервера; формат задокументирован в README.
