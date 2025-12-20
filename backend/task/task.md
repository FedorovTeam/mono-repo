## Что получится в итоге (кратко)

### 1) Архитектура остаётся «как в проекте»

- **Gateway**: GraphQL наружу + webhook MinIO, только оркестрация запросов и прокидывание `X-API-Key`/`x-correlation-id`.
- **Storage-service**: presigned URLs + Upload/GetObject, работа с MinIO, дефолтные бакеты/ключи.
- **Document-service**: **workflow engine** (таблицы `documents`, `document_versions`, `processing_jobs`) + StageWorker/JobFollower/Reconcilers.
- **Transformer-service**: очередь `transformations_queue`, Docling submit→poll→result, кладёт итоговый `.md` в MinIO.
- **Preprocess-service**: очередь `preprocess_queue`, LibreOffice конвертирует **doc/pdf→docx**, кладёт артефакт в MinIO.
- **Table-extractor-service**: очередь `table_extraction_queue`, Poppler (pdfinfo/pdftotext/pdftoppm) + ваш пайплайн галочек, кладёт:
    - итоговый `tables.md` (как markdown),
    - плюс набор артефактов (raw/normalized/manifest/debug) в prefix.
- **GitHub-publisher-service**: publish markdown в GitHub (idempotency cache по `idempotency_key`).

---

## Как именно интегрируем preprocess + table-extractor в текущий флоу

Сейчас у вас цепочка в `document-service`:

`Uploaded → StoredOriginal → ConvertedToMarkdown → PublishedToGitHub`

Добавляем **две новые стадии** в `processing_jobs` и **роутер** после `store_original`:

- `preprocess_to_docx`
- `extract_tables`

### Маршрутизация (основная идея)

- `.doc` → **preprocess_to_docx** → далее обычный `convert_to_markdown` (Transformer/Docling) уже на **docx**.
- `.pdf`:
    - если это “протокол/таблицы/галочки” → **extract_tables** (TableExtractor) → сразу получаем markdown (tables.md) и идём в publish
    - иначе (обычный pdf) → либо preprocess_to_docx → convert_to_markdown, либо прямо convert_to_markdown (как решите политикой/конфигом)

Эта логика подробно описана в **flows.md**, раздел “Маршрутизация конвертации (Router)”.

---

## Что нужно изменить/добавить (по сервисам)

Ниже — выжимка; полный пошаговый план — в **flows.md**, раздел “Что именно менять/добавлять в коде”.

### A) Document-service (ключевые изменения)

1. **Добавить новые stage**:
    - `preprocess_to_docx`
    - `extract_tables`
2. **StageWorker**:
    - Handler `preprocess_to_docx`:
        - первый проход: `SubmitPreprocess` → сохранить `preprocess_job_id` в payload → отложить задачу (pending+next_run_at)
        - следующие проходы: `GetPreprocessStatus` → ждать `COMPLETED|FAILED`
        - на `COMPLETED`: сохранить `preprocessed_bucket/key` в payload и завершить job
    - Handler `extract_tables`:
        - первый проход: `ExtractTablesFromPdf` → сохранить `table_queue_id`/`artifacts_prefix` → postpone
        - дальше: `GetTableExtractionJob`
        - на `done`: **зафиксировать ConvertedToMarkdown** на `tables.md` и завершить job
3. **convert_to_markdown** (существующий handler):
    - расширить резолвинг источника:
        - если в payload есть `preprocessed_bucket/key` → вызывать Transformer с `source_bucket/key` (или подменять `original_*` на уровне payload)
        - иначе — как сейчас
4. **JobFollower router**:
    - после завершения `store_original` выбирает следующий stage по матрице (doc/docx/pdf + стратегия)
5. **Хранение ссылок на артефакты**
    - минимум изменений: хранить всё в `processing_jobs.payload`:
        - `preprocess_job_id`, `preprocessed_bucket`, `preprocessed_key`
        - `table_queue_id`, `artifacts_bucket`, `artifacts_prefix`
    - опционально “красивее”: расширить `document_versions` полями для preprocess/tables (но это миграции + API изменения).

---

### B) Table-extractor-service (эволюция `pdf2md` → продуктовый сервис)

1. **Сервисная обвязка (как в mono-repo)**
- `cmd/main.go`, `internal/server` (gRPC + interceptors),
- `internal/repository` (очередь `table_extraction_queue`),
- `internal/worker` (workerLoop + maintenanceLoop),
- `internal/infrastructure/storage` (MinIO download/upload),
- `internal/extractor` (ваш pdf2md пайплайн).
1. **gRPC API**
- `ExtractTablesFromPdf(document_id, original_bucket, original_object_key, artifacts_bucket?, artifacts_prefix?, wait_for_result?)`
- `GetTableExtractionJob(queue_id)`
- (опц.) `ListTableExtractionJobs(...)`
1. **Артефакты**
- prefix по умолчанию: `tables/<document_id>/<queue_id>/`
- файлы:
    - `tables.raw.json`
    - `tables.normalized.json`
    - `tables.md` ✅ (итоговый markdown)
    - `manifest.json` (версии, параметры, метрики)
    - `debug/*` (опционально: ppm, страницы, координаты галочек)
1. **Важно для интеграции**
- Самый удобный вариант: TableExtractor **умеет писать итоговый markdown** сразу в `markdown_bucket/object_key`, который ему передал document-service (как Transformer делает).
- Параллельно TableExtractor пишет “пачку” debug/json в `artifacts_bucket`.

---

### C) Preprocess-service (доводим до стандартов проекта)

Если у вас уже есть сервис в нужном стиле — “допил” минимальный:

- идемпотентность (у вас она описана),
- queue_limit,
- maintenance цикл,
- чёткий контракт gRPC (`SubmitPreprocess`, `GetPreprocessStatus`),
- аккуратный режим работы LibreOffice: timeouts, чистка temp, ограничение параллелизма, стабильные ключи артефактов.

---

### D) Gateway-service (опционально, но полезно)

Добавить в GraphQL мутацию/метаданные флаги:

- `extractTables` (bool)
- `conversionStrategy` (enum: `default | tables_only | docling | preprocess_then_docling`)

Если не хотите менять контракт — можно держать правила в **конфиге роутера** document-service (по `repository/path`).

---

## Диаграммы, которые уже собраны в файлах

### sequence.md

- end‑to‑end upload → webhook → document workflow
- ветвление после `store_original`
- отдельные sequence по `preprocess_to_docx`, `extract_tables`, `convert_to_markdown`
- внутренности очередей preprocess/table-extractor/transformer (worker + maintenance)

### usecases.md

- единые стандарты: correlation, idempotency, очереди, ошибки gRPC
- use-cases по Gateway/Document/Transformer/Preprocess/TableExtractor/Publisher

### flows.md

- “целевая архитектура” компонентов
- матрица маршрутизации
- end‑to‑end flow с ветвлением
- **самое важное:** “что менять/добавлять” — конкретный план работ по сервисам, БД, контрактам, конфигам

---

## Что я рекомендую внедрять в каком порядке (чтобы не сломать прод)

1. **table-extractor-service как отдельный сервис** (очередь + воркер + артефакты) без включения в документ‑workflow.
2. Добавить в document-service stage `extract_tables` и включать роутинг **только для одного репозитория/пути** через конфиг.
3. Добавить stage `preprocess_to_docx` и включить только для `.doc` (это обычно самая безопасная польза).
4. Дальше расширять правила роутера на `.pdf` (обычные pdf → preprocess/docling; протоколы → table extractor).