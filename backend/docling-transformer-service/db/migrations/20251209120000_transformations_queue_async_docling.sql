-- +goose Up
-- +goose StatementBegin

-- 1. Базовая таблица очереди (на случай чистой базы).
CREATE TABLE IF NOT EXISTS transformations_queue (
                                                     id UUID PRIMARY KEY,
                                                     status TEXT NOT NULL,
                                                     document_id TEXT NOT NULL,
                                                     original_bucket TEXT NOT NULL,
                                                     original_object_key TEXT NOT NULL,
                                                     markdown_bucket TEXT NOT NULL,
                                                     markdown_object_key TEXT NOT NULL,
                                                     filename_extension TEXT,
                                                     created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
                                                     started_at TIMESTAMPTZ,
                                                     finished_at TIMESTAMPTZ,
                                                     error TEXT
);

-- 2. Приводим статус к расширенному CHECK с waiting_docling.
ALTER TABLE transformations_queue
    DROP CONSTRAINT IF EXISTS transformations_queue_status_check,
    ADD CONSTRAINT transformations_queue_status_check
CHECK (status IN ('pending', 'processing', 'waiting_docling', 'done', 'failed'));

-- 3. Новые служебные поля для ретраев, корреляции и Docling.
ALTER TABLE transformations_queue
    ADD COLUMN IF NOT EXISTS retry_count INT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS correlation_id TEXT,
    ADD COLUMN IF NOT EXISTS docling_job_id TEXT,
    ADD COLUMN IF NOT EXISTS docling_poll_count INT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS next_poll_at TIMESTAMPTZ;

-- 4. Индекс для быстрого выбора следующей задачи по статусу/дате.
--    Типичный запрос: SELECT ... FROM ... WHERE status='pending' ORDER BY created_at LIMIT 1;
CREATE INDEX IF NOT EXISTS transformations_queue_status_idx
    ON transformations_queue (status, created_at);

-- 5. Индекс для поиска "зависших" задач в статусе processing.
--    Запросы вида: WHERE status='processing' AND started_at < now()-interval '...';
CREATE INDEX IF NOT EXISTS transformations_queue_started_at_idx
    ON transformations_queue (started_at)
    WHERE status = 'processing';

-- 6. Индекс по docling_job_id (используется в UpdateDoclingInfo / поиске по job’у).
CREATE INDEX IF NOT EXISTS transformations_queue_docling_idx
    ON transformations_queue (docling_job_id)
    WHERE docling_job_id IS NOT NULL;

-- 7. Индекс по next_poll_at для статуса waiting_docling.
--    Запросы вида: WHERE status='waiting_docling' AND next_poll_at <= now() ORDER BY next_poll_at LIMIT N;
CREATE INDEX IF NOT EXISTS transformations_queue_waiting_idx
    ON transformations_queue (next_poll_at)
    WHERE status = 'waiting_docling';

-- +goose StatementEnd



-- +goose Down
-- +goose StatementBegin

-- 1. На всякий случай переводим waiting_docling обратно в pending,
--    чтобы не нарушить более строгий CHECK ниже.
UPDATE transformations_queue
SET status = 'pending'
WHERE status = 'waiting_docling';

-- 2. Возвращаем старый CHECK без waiting_docling.
ALTER TABLE transformations_queue
    DROP CONSTRAINT IF EXISTS transformations_queue_status_check,
    ADD CONSTRAINT transformations_queue_status_check
CHECK (status IN ('pending', 'processing', 'done', 'failed'));

-- 3. Чистим индексы, связанные с асинхронным Docling.
DROP INDEX IF EXISTS transformations_queue_waiting_idx;
DROP INDEX IF EXISTS transformations_queue_docling_idx;
DROP INDEX IF EXISTS transformations_queue_started_at_idx;
DROP INDEX IF EXISTS transformations_queue_status_idx;

-- 4. Удаляем добавленные поля.
ALTER TABLE transformations_queue
    DROP COLUMN IF EXISTS next_poll_at,
DROP COLUMN IF EXISTS docling_poll_count,
    DROP COLUMN IF EXISTS docling_job_id,
    DROP COLUMN IF EXISTS correlation_id,
    DROP COLUMN IF EXISTS retry_count;

-- Таблицу целиком не удаляем, чтобы не потерять данные очереди.

-- +goose StatementEnd
