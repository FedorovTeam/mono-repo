-- +goose Up
-- +goose StatementBegin
ALTER TABLE transformations_queue
    ADD COLUMN IF NOT EXISTS docling_task_id TEXT;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE transformations_queue
    DROP COLUMN IF EXISTS docling_task_id;
-- +goose StatementEnd
