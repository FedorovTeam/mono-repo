# usecases.md — use-cases, контракты и правила (лучшие практики проекта)

Ниже — **целевая архитектура use-cases** для всего проекта и интеграции `preprocess-service` + `table-extractor-service` в существующий workflow.

---

## 1) Глобальные стандарты проекта (обязательные)

### 1.1 Корреляция, логирование, трассировка
- `x-correlation-id` читается из входящих заголовков/metadata, иначе генерируется.
- Возвращается в ответе и **прокидывается**:
  - во все gRPC вызовы downstream,
  - в Docling HTTP (`X-Correlation-Id`),
  - в GitHub HTTP (header из конфига, по умолчанию `X-Correlation-ID`),
  - в MinIO metadata (`x-amz-meta-correlation-id`) где применимо.
- Логи всех сервисов: структурированные поля `correlation_id`, `document_id`, `job_id/queue_id`, `stage`, `attempt`.

### 1.2 Идемпотентность
- По возможности — **idempotent by key**:
  - DocumentService: `CreateDocument` идемпотентен по `document_id` (если явно передан), иначе генерируется.
  - Transformer/TableExtractor: «последняя задача по `document_id` не в `failed`» возвращается повторно.
  - Preprocess: идемпотентность по `(source_bucket, source_key, source_etag)` (или `(bucket,key,versionId)` если появится).
  - Publisher: идемпотентность по `idempotency_key` (кэш только для успешных публикаций).

### 1.3 Очереди + воркеры
- Очередь — таблица в PostgreSQL, воркеры берут задачи через `SELECT ... FOR UPDATE SKIP LOCKED`.
- Статусы: `pending` → `processing` → `done|failed` (+ допускаются `waiting_*` для внешних систем).
- Maintenance цикл:
  - `processing` после рестарта → `pending`;
  - зависшие `processing` по `processing_timeout` → `pending` (или `failed` если лимиты);
  - истёкшие `waiting_*` по `max_job_lifetime` → `failed`;
  - retry `failed` по `failed_retry_delay` в пределах `max_failed_retries`;
  - cleanup `done/failed` по `cleanup_retention`.

### 1.4 Ошибки и gRPC коды (единый словарь)
- `InvalidArgument`: отсутствие обязательных полей / конфликтующие поля / неподдерживаемое расширение / пустой ответ от HTTP.
- `FailedPrecondition`: не сконфигурирован storage/minio клиент при необходимости / недоступны обязательные ресурсы.
- `ResourceExhausted`: превышен `queue_limit` активных задач.
- `Unavailable`: временная недоступность downstream (прокидывается/мапится).
- `DeadlineExceeded`: таймаут ожидания (`wait_for_result`) или внутренние timeouts IO.
- `Internal`: непредвиденные ошибки, баги, неконсистентность данных.

---

## 2) Use-cases: Gateway-service (GraphQL)

### 2.1 UploadKnowledgeDocument (подписанная загрузка)
**Акторы:** Client → Gateway → StorageService → DocumentService → MinIO webhook.

**Валидация:**
- `X-API-Key` обязателен.
- `fileName`, `repository`, `branch`, `fileSizeBytes>0` обязательны.
- `path` может быть пустым/whitespace → подставить `knowledge/docs/<documentId>.md`.
- Опционально прокидываются флаги стратегии: `extractTables` (bool) и `conversionStrategy` (`DEFAULT|TABLES_ONLY|DOCLING_WITH_TABLES`), которые сохраняются в document-service и позволяют принудительно включить Docling → TableExtractor.

**Результат:**
- `uploadUrl`, `uploadMethod=PUT`, `document` (status=UPLOADED), `X-Correlation-ID` в ответе.

### 2.2 Webhook `/internal/storage/uploaded`
**Акторы:** MinIO → Gateway → DocumentService.

**Правила:**
- Обрабатывать все records, где удаётся извлечь `document_id` из имени объекта.
- `correlation-id` брать из `x-amz-meta-correlation-id`, иначе генерировать.
- По каждой записи вызывать `ConfirmUpload`.

---

## 3) Use-cases: Document-service (workflow engine)

Document-service остаётся «оркестратором» стадий через `processing_jobs` + воркеры.

### 3.1 CreateDocument
**Валидация:**
- UUID (если передан), корректность `repository` (в виде `owner/repo` допускается, но без конфликтов).
- `upload_object_key` и `markdown_object_key` **обязательны** (дефолты — только для bucket).

**Сторедж:**
- В этом методе **не** ходим в MinIO; только сохраняем метаданные.

### 3.2 ConfirmUpload
**Валидация:**
- UUID документа, совпадение bucket/key с сохранёнными (если заданы).
- Проверка наличия объекта через `storage-service.GetObject/ValidateObject`.

**Эффект:**
- Добавляет задачу `processing_jobs(stage=store_original,status=pending)` и событие `scheduled`.

### 3.3 StageWorker (store_original)
**Эффект:** `Uploaded -> StoredOriginal`, версия/лог.

### 3.4 JobFollower (branching router) — **новое/расширенное**
После успешного `store_original` выбираем следующий stage по стратегии:

**Входные параметры стратегии:**
- расширение (`docx|doc|pdf`),
- конфиг `router.preprocess_enabled`, `router.table_extractor_enabled`,
- метаданные документа: `conversion_strategy` (опционально), `extract_tables` (опционально).

**Правила маршрута (рекомендовано):**
1) `docx` → `convert_to_markdown` (Transformer).  
2) `doc` → `preprocess_to_docx` → `convert_to_markdown`.  
3) `pdf`:
   - если `extract_tables=true` или `conversion_strategy=tables_only` → `extract_tables` (TableExtractor) → `publish_to_github`;
   - иначе если `preprocess_enabled=true` → `preprocess_to_docx` → `convert_to_markdown`;
   - иначе → `convert_to_markdown` напрямую (Transformer, source_async/download по конфигу).

> Такой роутер позволяет «переключать» PDF протоколы в table-extractor без ломки общего API.

### 3.5 StageWorker (preprocess_to_docx) — **новая стадия**
**Акторы:** Document-service worker → preprocess-service.

**Валидация payload:**
- `source_bucket/key` обязательны.
- `target_bucket/key` опциональны (иначе дефолт в preprocess-service).
- Если известен `etag` оригинала — передавать для защиты от race.

**Поведение:**
- Первый запуск: `SubmitPreprocess`, сохранить `preprocess_job_id` в payload.
- Повторные запуски: `GetPreprocessStatus` по `job_id`.
- На `COMPLETED`: сохранить `preprocessed_bucket/key` в payload и завершить stage.
- На `FAILED`: завершить stage `failed` и заполнить `payload.last_error`.

### 3.6 StageWorker (convert_to_markdown)
**Изменение:** выбирать источник:
- если `payload.preprocessed_bucket/key` есть → он становится `original_*` для Transformer;
- иначе используем исходный `original_*` из документа.

Остальная логика (idempotent, postpone, backoff) — как сейчас.

### 3.7 StageWorker (extract_tables) — **новая стадия**
**Акторы:** Document-service worker → table-extractor-service.

**Поведение:**
- Первый запуск: `ExtractTablesFromPdf(wait=false)` сохранить `queue_id` и `artifacts_prefix`.
- Повторные: `GetTableExtractionJob(queue_id)`.
- На `done`:
  - считать `tables.md` «готовым Markdown»,
  - вызвать `MarkConvertedToMarkdown` так же, как в `convert_to_markdown`, но `markdown_object_key = <artifacts_prefix>/tables.md`.
- Далее JobFollower планирует `publish_to_github` как обычно.

### 3.8 publish_to_github
Без изменений (требования к `commit_message`, `author_*`, idempotency_key).

---

## 4) Use-cases: Preprocess-service (LibreOffice → DOCX)

### 4.1 SubmitPreprocess
**Валидация:**
- `document_id` обязателен.
- `source_bucket/source_key` обязательны.
- `source_etag` опционален.
- `target_bucket/target_key` опциональны (иначе из конфига).
- `callback_url` опционален (должен быть валидным URL, если включено).

**Идемпотентность:**
- Если уже существует запись с тем же `(source_bucket,source_key,source_etag)` и не `FAILED` → вернуть её `job_id` и статус.

### 4.2 GetPreprocessStatus
Возвращает статус + `target_bucket/key` при `COMPLETED` + `error_code/message` при `FAILED`.

**Ошибки:**
- `InvalidArgument`: невалидные поля, исходник не найден, etag mismatch.
- `ResourceExhausted`: переполнена очередь (`queue_limit`).

---

## 5) Use-cases: Table-extractor-service (Poppler → tables.md)

### 5.1 ExtractTablesFromPdf
**Валидация:**
- `document_id`, `original_bucket`, `original_object_key` обязательны.
- Только PDF (иначе `InvalidArgument`).
- `artifacts_bucket` из запроса или конфига (если оба пустые — `InvalidArgument`).
- `queue_limit` учитывает `pending/processing` (и/или `waiting_*`, если появится).

**Идемпотентность:**
- Возвращаем последнюю задачу по `document_id` не в `failed`.

### 5.2 Worker pipeline (алгоритм)
- `pdfinfo`: проверить шифрование/битость/число страниц.
- `pdftotext -layout`: получить «слой текста» и координатный порядок.
- `pdftoppm`: рендер страниц/фрагментов для извлечения «галочек» (если режим voting_tables включён).
- Парсинг/нормализация таблиц:
  - определить границы строк/колонок,
  - сопоставить галочки с ячейками,
  - нормализовать заголовки и значения,
  - сгенерировать:
    - `tables.raw.json` (сырой),
    - `tables.normalized.json` (нормализованный),
    - `tables.md` (читаемый Markdown),
    - `manifest.json` (метрики/версия/параметры),
    - `debug/*` (опционально).

### 5.3 GetTableExtractionJob
Отдаёт статус и пути артефактов.

---

## 6) Use-cases: Transformer-service (Docling)

Остаётся как в текущем описании (очередь `transformations_queue`, статусы `pending/processing/waiting_docling/done/failed`), с опциональным улучшением:

**Рекомендация:** добавить поле `original_media_type`/`source_filename` в запись очереди для точности диагностики и повторов.

---

## 7) Набор изменений по контрактам (минимально инвазивно)

### 7.1 Document-service
- **Не обязательно** менять публичные gRPC контракты для клиентов.
- Достаточно:
  - добавить новые `stage` значения в `processing_jobs` (как строки),
  - расширить payload для хранения `preprocess_job_id`, `preprocessed_bucket/key`, `table_queue_id`, `artifacts_prefix`,
  - добавить router правила в `JobFollower`.

### 7.2 Gateway GraphQL (опционально)
- Добавить входные поля:
  - `conversionStrategy: DEFAULT|TABLES_ONLY|DOCX_THEN_DOCLING`,
  - `extractTables: Boolean`,
  - (или по репозиторию/пути включать правило на бэкенде).
- Эти флаги сохранять в document метаданных, которые читает router.

---

## 8) Best practices checklist (для PR-ревью)
- [ ] Все методы возвращают `x-correlation-id`.
- [ ] Везде есть idempotency: нет дублей задач при ретраях.
- [ ] Любой внешний вызов имеет timeout и классификацию ошибок (retryable vs permanent).
- [ ] Очереди обслуживаются maintenance циклом; рестарт безопасен.
- [ ] Артефакты пишутся атомарно (временный ключ → rename/copy, если нужно) или с `manifest.json` как «флаг готовности».
- [ ] Метрики: `queue_depth{status}`, `tasks_completed_total`, `tasks_failed_total`, `task_duration_seconds`.
- [ ] Документация: `sequence.md/usecases.md/flows.md` синхронизированы с кодом и протоколами.
