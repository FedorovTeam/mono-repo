# Контракты

## gRPC
Контракт описан в `proto/backend/document/document.proto`. Основные методы:
- `CreateDocument` — создание записи с генерацией ключей.
- `GetDocument` — получение по ID.
- `ListDocuments` — пагинация и фильтры по статусу.
- `UpdateDocumentStatus` — обновление статуса с валидацией переходов.

## MinIO
- Для каждого документа генерируются ключи в указанных бакетах: базовый путь `documents/<uuid>[.<ext>]` используется для Markdown, оригинал сохраняется по пути `documents/<uuid>[.<ext>]/original`.
- Фактическая загрузка вне сервиса; мы храним только ключи и записываем их в базу.

## БД
- Таблицы `documents`, `document_versions`, `document_status_logs` (см. миграции в `db/migrations`).
