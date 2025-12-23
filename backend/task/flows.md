# flows.md — бизнес‑флоу, маршрутизация и что менять в коде

Ниже — «целевая» архитектура всего проекта с интеграцией `preprocess-service` и `table-extractor-service` в существующий workflow Upload → Convert → Publish.

---

## 1) Архитектура проекта (целевая)

### 1.1 Сервисы и ответственность

| Компонент | Роль | Хранилище | Внешние зависимости |
|---|---|---|---|
| gateway-service | GraphQL API + webhooks MinIO | stateless | document-service, storage-service |
| storage-service | presigned URLs + UploadOriginal/Markdown + GetObject | MinIO | MinIO |
| document-service | workflow engine (documents + processing_jobs) | Postgres | transformer, preprocess, table-extractor, publisher, storage |
| transformer-service | очередь конвертаций в Markdown через Docling | Postgres | Docling, MinIO |
| preprocess-service | очередь конвертаций в DOCX через LibreOffice | Postgres | MinIO, LibreOffice |
| table-extractor-service | очередь детерминированного извлечения таблиц из PDF | Postgres | MinIO, Poppler |
| github-publisher-service | commit/PR в GitHub (идемпотентность) | (cache) | GitHub API |
| Docling | внешняя конвертация (async submit/poll/result) | — | — |
| MinIO | объектное хранилище | — | — |
| PostgreSQL | метаданные и очереди | — | — |

### 1.2 Дизайн-принципы (как в проекте)
- **Чёткие границы:** сервис владеет своей БД/очередью; другие не лезут напрямую.
- **gRPC как основной транспорт** между сервисами (Gateway — GraphQL только наружу).
- **Очереди в Postgres** для CPU/IO тяжёлых задач, нагрузка контролируется `queue_limit/max_workers`.
- **Idempotency everywhere**: повторный вызов не создаёт дубликаты.
- **Correlation-first**: единая трассировка по `x-correlation-id`.
- **Config-driven**: один `config.yaml` на сервис, `CONFIG_PATH` как в остальных.

---

## 2) Маршрутизация конвертации (Router) — ключ к интеграции

### 2.1 Матрица решений

| Входной формат | Условие | Следующий stage | Выход |
|---|---|---|---|
| docx | всегда | convert_to_markdown (Transformer) | markdown |
| doc | preprocess.enabled=true | preprocess_to_docx → convert_to_markdown | markdown |
| doc | preprocess.disabled | convert_to_markdown (если Docling справляется) | markdown |
| pdf | extract_tables=true / tables_only | extract_tables (TableExtractor) | tables.md (markdown) |
| pdf | иначе + preprocess.enabled | preprocess_to_docx → convert_to_markdown | markdown |
| pdf | иначе | convert_to_markdown (Transformer) | markdown |

### 2.2 Где хранить настройки стратегии
Рекомендовано (по «лучшим практикам» проекта):
1) **Конфиг роутера** в document-service:
   - `router.preprocess_enabled`
   - `router.table_extractor_enabled`
   - правила по `repository/path` (например, `knowledge/voting/**`).
2) **Опциональные флаги документа**, задаваемые Gateway:
   - `extract_tables` (bool)
   - `conversion_strategy` (enum)

> Это позволяет без релизов клиента переводить конкретные репозитории/папки на table-extractor.

---

## 3) End-to-end поток: Upload → Convert → Publish (с ветвлением)

### 3.1 Базовый happy path
1. Клиент вызывает GraphQL `uploadKnowledgeDocument`.
2. Gateway получает presigned URL у storage-service и создаёт документ в document-service.
3. Клиент загружает файл в MinIO.
4. MinIO webhook → Gateway → `ConfirmUpload` в document-service.
5. document-service ставит `store_original`.
6. StageWorker выполняет `store_original`.
7. JobFollower роутит следующий stage:
   - `docx` → `convert_to_markdown`
   - `doc/pdf` → `preprocess_to_docx` (если включено)
   - `pdf протокол` → `extract_tables`
8. Когда появляется Markdown (из Transformer или TableExtractor), документ помечается `ConvertedToMarkdown` и ставится `publish_to_github`.
9. GitHubPublisher публикует и возвращает `commit_sha`/PR.

### 3.2 Mermaid flow (вся цепочка)

```mermaid
flowchart TD
  A[UploadKnowledgeDocument] --> B[PUT to MinIO]
  B --> C[MinIO webhook]
  C --> D[ConfirmUpload -> store_original job]
  D --> E[StageWorker: store_original -> StoredOriginal]

  E --> R{Router by ext/flags}
  R -->|docx| M[convert_to_markdown (Transformer)]
  R -->|doc/pdf + preprocess| P[preprocess_to_docx (Preprocess)]
  R -->|pdf + tables| T[extract_tables (TableExtractor)]
  R -->|fallback| M

  P --> M
  M --> Z[ConvertedToMarkdown]
  T --> Z

  Z --> G[publish_to_github (Publisher)]
  G --> H[PublishedToGitHub]
```

---

## 4) Что именно менять/добавлять в коде (план работ)

### 4.1 Document-service: новые стадии в processing_jobs

**Добавить stage enum (или строковые константы):**
- `preprocess_to_docx`
- `extract_tables`

**StageWorker:**
- новый handler `preprocess_to_docx`:
  - первый запуск → `SubmitPreprocess`, сохранить `preprocess_job_id` в payload
  - далее → `GetPreprocessStatus`, пока `COMPLETED|FAILED`
  - на `COMPLETED` — сохранить `preprocessed_bucket/key` в payload и закрыть job
- новый handler `extract_tables`:
  - первый запуск → `ExtractTablesFromPdf`, сохранить `queue_id`/`artifacts_prefix`
  - далее → `GetTableExtractionJob`
  - на `done` — вызвать `MarkConvertedToMarkdown` с `tables.md` и закрыть job

**convert_to_markdown handler:**
- расширить резолвер входного файла:
  - если `payload.preprocessed_bucket/key` задан — использовать его как `original_*` для TransformerService
  - иначе — использовать исходник

**JobFollower router:**
- после `store_original completed` добавить ветвление по матрице из раздела 2.1.

**Схема данных:**
- Минимально-инвазивно: хранить всё в `processing_jobs.payload`:
  - `preprocess_job_id`, `preprocessed_bucket`, `preprocessed_key`
  - `table_queue_id`, `artifacts_bucket`, `artifacts_prefix`
- Опционально (лучше для аналитики): расширить `document_versions`:
  - `preprocess_bucket/key`
  - `tables_bucket/key`
  - но это потребует миграции + обновления API ответа.

### 4.2 Gateway-service (GraphQL)
- GraphQL-мутация `uploadKnowledgeDocument` принимает поля `extractTables` и `conversionStrategy`, чтобы клиент мог принудительно включить цепочку Docling → TableExtractor.
- Значения сохраняются через `DocumentService.CreateDocument` и используются router’ом document-service при выборе стадий.

### 4.3 Transformer-service (без критичных изменений)
- Оставить существующий API и очередь.
- Рекомендовано добавить в ответе (и в записи очереди) диагностические поля: `source_filename`, `resolved_media_type`.

### 4.4 Preprocess-service (допил до «продукта»)
Чтобы сервис соответствовал стандартам mono-repo:

**gRPC API:**
- `SubmitPreprocess` (idempotent, queue_limit)
- `GetPreprocessStatus`
- (опционально) `ListPreprocessJobs`

**DB:**
- `preprocess_queue` с полями:
  - `job_id`, `document_id`
  - `source_bucket/key`, `source_etag`
  - `target_bucket/key`
  - `status`, `attempt`, `error_code/message`
  - `created_at/updated_at/finished_at`
  - `next_run_at` (для postpone/backoff)

**Worker:**
- `io_timeout` на загрузку/выгрузку
- `convert_timeout` на LibreOffice
- ретраи на временные IO ошибки
- maintenance как в других очередях

**Артефакты и ключи:**
- рекомендуемый ключ:
  - `preprocess/<document_id>/<job_id>/document.docx`
- `manifest.json` опционально (версии LO, время конвертации, размеры).

### 4.5 Table-extractor-service: из pdf2md в сервис

**Шаг 1: выделить доменный пайплайн**
- `internal/extractor`:
  - `pdfinfo` check
  - `pdftotext` extraction
  - `pdftoppm` images (для галочек)
  - parser/normalizer
  - renderer (md/json/manifest)

**Шаг 2: сервисная обвязка как у остальных**
- `cmd/main.go` + `internal/server` (gRPC + interceptors)
- `internal/repository` (table_extraction_queue)
- `internal/worker` (queue + maintenance)
- `internal/infrastructure/storage` (MinIO download/upload)

**Артефакты**
- prefix: `tables/<document_id>/<queue_id>/`
- файлы:
  - `tables.raw.json`
  - `tables.normalized.json`
  - `tables.md`
  - `manifest.json`
  - `debug/*`

**Config**
- `extractor.mode` (layout/structured)
- `extractor.detect_voting_tables=true|false`
- `extractor.pages=all|1-3`
- `service.queue_limit`, `service.max_workers`, `service.processing_timeout`
- `scheduler.*` (poll_interval, maintenance_interval, batch_size, retry_backoff, cleanup_retention)

---

## 5) Набор диаграмм/доков, которые нужно держать в репо

1) `docs/sequence.md` — последовательности (как в этом файле)  
2) `docs/usecases.md` — контракты, валидации, статусы, ошибки  
3) `docs/flows.md` — бизнес‑флоу и router матрица  

Рекомендация: завести `docs/architecture/` и линковать эти файлы из README сервисов.

---

## 6) Нефункциональные требования (чтобы не было регрессий)

### 6.1 Производительность и лимиты
- `queue_limit` ограничивает активные задачи (на каждый сервис отдельно).
- `max_workers` ограничивает параллелизм.
- У тяжёлых утилит (LibreOffice/Poppler) — ограничить concurrency по CPU (часто 1..N).

### 6.2 Надёжность
- Любой «внешний» шаг должен быть повторяемым (idempotent) или иметь защиту от дублей.
- При записи артефактов: `manifest.json` как «атомарный признак готовности» (пишется последним).
- Везде есть таймауты на IO/HTTP.

### 6.3 Безопасность
- Никаких MinIO кредов в публичных запросах.
- MinIO креды берутся из config/K8s secrets.
- Если есть работа с внешним `source_url`, ограничить allowlist доменов или включать только по флагу.

---

## 7) Минимальный план внедрения по итерациям (без остановки продакшена)

1) **Итерация 1:** table-extractor-service как самостоятельный сервис + ручной вызов из internal tooling.  
2) **Итерация 2:** добавить stage `extract_tables` в document-service и feature-flag роутера по repository/path.  
3) **Итерация 3:** preprocess-service интегрировать как stage `preprocess_to_docx` для `.doc` (и затем для `.pdf` где нужно).  
4) **Итерация 4:** добавить GraphQL флаг `extractTables`/`conversionStrategy` (если нужна ручная маршрутизация клиентом).  
5) **Итерация 5:** расширить `document_versions` для явного хранения preprocess/tables артефактов (если нужно в UI/аудите).  
