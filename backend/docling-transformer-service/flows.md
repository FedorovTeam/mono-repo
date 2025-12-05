# Flows

## Конвертация DOCX → Markdown
1. Gateway/Temporal вызывает gRPC метод `ConvertDocxToMarkdown` с `document_id`, `original_bucket`, `original_object_key` и `x-correlation-id` (опционально `markdown_bucket`/`markdown_object_key`).
2. Интерсептор сохраняет/генерирует correlation-id и пишет входные метаданные в лог.
3. Сервис скачивает исходный DOCX из MinIO (через MinIO SDK), после чего `DoclingConverter` отправляет POST `${docling.endpoint}/convert`, прокидывая `X-Correlation-Id`, `X-Source-Filename` и `X-Docling-Endpoint`.
4. Docling возвращает Markdown-текст; сервис загружает его в бакет Markdown (по умолчанию из `config.yaml`).
5. gRPC-слой отдаёт `markdown`, `markdown_bucket` и `markdown_object_key` вызывающей стороне; correlation-id используется для стыковки со шагами публикации в GitHub (в перспективе через Temporal workflow).

## Управление конфигурацией
1. При старте `cmd/server` читает `config.yaml` (или путь из `CONFIG_PATH`).
2. Все интеграции (Docling, MinIO, Temporal) берут параметры из конфига, без `.env`.
3. Любые изменения конфига требуют перезапуска сервера; формат задокументирован в README.
