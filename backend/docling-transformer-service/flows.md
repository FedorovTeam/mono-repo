# Flows

## Конвертация DOCX → Markdown
1. Gateway/Temporal вызывает gRPC метод `ConvertDocxToMarkdown` с байтами DOCX и `x-correlation-id`.
2. Интерсептор сохраняет/генерирует correlation-id и пишет входные метаданные в лог.
3. `DoclingConverter` отправляет POST `${docling.endpoint}/convert` в Docling-конвертер, прокидывая `X-Correlation-Id`, `X-Source-Filename` и `X-Docling-Endpoint`.
4. Docling возвращает Markdown-текст.
5. gRPC-слой отдаёт `markdown` вызывающей стороне; correlation-id используется для стыковки со шагами хранения в MinIO и публикации в GitHub (в перспективе через Temporal workflow).

## Управление конфигурацией
1. При старте `cmd/server` читает `config.yaml` (или путь из `CONFIG_PATH`).
2. Все интеграции (Docling, MinIO, Temporal) берут параметры из конфига, без `.env`.
3. Любые изменения конфига требуют перезапуска сервера; формат задокументирован в README.
