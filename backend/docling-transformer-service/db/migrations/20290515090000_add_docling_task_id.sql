-- +goose Up
-- +goose StatementBegin
ALTER TABLE transformations_queue
    ADD COLUMN IF NOT EXISTS docling_task_id TEXT;

CREATE INDEX IF NOT EXISTS transformations_queue_docling_task_idx
    ON transformations_queue (docling_task_id)
    WHERE docling_task_id IS NOT NULL;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS transformations_queue_docling_task_idx;
ALTER TABLE transformations_queue
    DROP COLUMN IF EXISTS docling_task_id;
-- +goose StatementEnd
