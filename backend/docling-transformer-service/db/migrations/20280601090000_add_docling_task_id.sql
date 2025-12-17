-- +goose Up
-- Добавляем docling_task_id для хранения task_id от Docling наряду с job_id.
ALTER TABLE transformations_queue
    ADD COLUMN IF NOT EXISTS docling_task_id TEXT;

-- Индекс для быстрого доступа по task_id при поллинге статусов.
CREATE INDEX IF NOT EXISTS transformations_queue_docling_task_idx
    ON transformations_queue (docling_task_id)
    WHERE docling_task_id IS NOT NULL;

-- +goose Down
DROP INDEX IF EXISTS transformations_queue_docling_task_idx;
ALTER TABLE transformations_queue
    DROP COLUMN IF EXISTS docling_task_id;
