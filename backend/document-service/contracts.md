# Контракты

## gRPC
Контракт описан в `proto/backend/document/document.proto`. Основные методы:
- `CreateDocument` — создание записи с генерацией ключей.
- `GetDocument` — получение по ID.
- `ListDocuments` — пагинация и фильтры по статусу.
- `UpdateDocumentStatus` — обновление статуса с валидацией переходов.

## MinIO / storage-service
- Для каждого документа `storage-service` формирует ключи по шаблону `org/<org_slug>/project/<project_slug>/year=YYYY/month=MM/day=DD/<uuid>/<document_id>.<ext>` для оригинала и `.../<uuid>/<document_id>.md` для Markdown.
- Фактическая загрузка выполняется через RPC `UploadOriginal` / `UploadMarkdown` `storage-service`, document-service сохраняет только бакеты и ключи, которые возвращает этот сервис.

## БД
- Таблицы `documents`, `document_versions`, `document_status_logs` (см. миграции в `db/migrations`).

## Очередь `processing_jobs`: payload по стадиям

Каждый cron-воркер берёт задачу из таблицы `processing_jobs` (`stage`, `status`, `payload` JSONB) и восстанавливает контекст шага без дополнительных запросов. Поля дублируют ключевые столбцы таблицы `documents` (бакеты, ключи, GitHub‑мета) и внешние идентификаторы для трассировки.

### `store_original`
- **Назначение:** зафиксировать, что исходник есть в MinIO и можно переводить документ в `StoredOriginal`.
- **Обязательные поля:**
  - `bucket` — MinIO bucket оригинала (`documents.original_bucket`).
  - `object_key` — путь к загруженному файлу (`documents.original_object_key`).
- **Опциональные поля:**
  - `correlation_id` — протягивается в логи/метрики воркера.
- **Пример:**
  ```json
  {
    "bucket": "knowledge-original",
    "object_key": "org/knowledge/project/documents/year=2024/month=05/day=14/7c1.../report.docx",
    "correlation_id": "7f3d7c8b-1c3f-4a4c-9f2e-8c6a1e8dbbaf"
  }
  ```
- **Связь с хранилищами:** воркер проверяет наличие `bucket/object_key` в MinIO и после успеха пишет событие в `job_events`, переводя документ в `StoredOriginal`.

### `convert_to_markdown`
- **Назначение:** подготовить входные данные для вызова Docling через `storage-service`/`transformer-service`.
- **Обязательные поля:**
  - `original_bucket`, `original_object_key` — откуда читать DOCX/PDF (`documents.original_*`).
  - `markdown_bucket`, `markdown_object_key` — куда писать результат (`documents.markdown_*`, обновляется версией). Если `markdown_object_key` пуст, воркер использует ключ из `documents`.
- **Опциональные поля:**
  - `correlation_id` — прокидывается в gRPC‑метаданные (`x-correlation-id`).
  - `queue_id` — идентификатор очереди/таски конвертера, если она уже была создана.
- **Пример:**
  ```json
  {
    "original_bucket": "knowledge-original",
    "original_object_key": "org/knowledge/project/documents/year=2024/month=05/day=14/7c1.../report.docx",
    "markdown_bucket": "knowledge-md",
    "markdown_object_key": "org/knowledge/project/documents/year=2024/month=05/day=14/7c1.../report.md",
    "correlation_id": "7f3d7c8b-1c3f-4a4c-9f2e-8c6a1e8dbbaf",
    "queue_id": "docling-42"
  }
  ```
- **Связь с хранилищами:** после успешного `StatObject` исходника воркер получает байты через `storage-service`, отправляет в Docling и сохраняет ключ Markdown в MinIO и в `document_versions`/`documents`.

### `publish_to_github`
- **Назначение:** передать все данные для публикации Markdown в GitHub.
- **Обязательные поля:**
  - `markdown_bucket`, `markdown_object_key` — что читать из MinIO (сверяется с `documents.markdown_*`).
  - `repository` — `owner/repo` или `documents.repository`.
  - `branch` — целевая ветка (`documents.branch`).
  - `github_path` — путь в репозитории, обычно равен `documents.path` или `publish_path`.
- **Опциональные поля:**
  - `create_pr` — флаг авто‑PR (по умолчанию `documents.create_pr`).
  - `base_branch` — ветка для PR, если отличается от целевой.
  - `correlation_id` — прокидывается в GitHub‑клиент и логи.
  - `attempt` — счётчик повторов публикации (увеличивается в `processing_jobs.attempts`), передаётся в `PublishMarkdown`.
- **Пример:**
  ```json
  {
    "markdown_bucket": "markdown",
    "markdown_object_key": "documents/2024/05/14/7c1.../report.md",
    "repository": "org/knowledge-base",
    "branch": "feature/docs-import",
    "base_branch": "main",
    "github_path": "knowledge/report.md",
    "create_pr": true,
    "correlation_id": "7f3d7c8b-1c3f-4a4c-9f2e-8c6a1e8dbbaf"
  }
  ```
- **Связь с GitHub:** воркер скачивает Markdown из MinIO, формирует коммит/PR через `github-publisher-service` и по успеху фиксирует статус `PublishedToGitHub` и событие в `job_events`.
- **Обработка ошибок:** `github-publisher-service` возвращает нормализованные статусы (`SUCCESS`, `TEMPORARY_FAILURE`, `PERMANENT_FAILURE`) и коды ошибок; воркер сохраняет их в `documents.error_reason` без дополнительного парсинга HTTP-кодов. Поле `retry_after`, вычисленное по `github_retryer.initial_backoff`/`max_backoff` и `attempt`, логируется и используется для расчёта задержки перед повторной попыткой.
- **Идемпотентность:** для каждого документа генерируется `idempotency_key` (`document_id + branch + markdown_object_key`), который прокидывается в `PublishMarkdown`. Повторный запрос с тем же ключом не создаёт дублирующих коммитов/PR.
