-- +goose Up
-- +goose StatementBegin
ALTER TABLE transformations_queue
    ADD COLUMN IF NOT EXISTS overlay_stage TEXT,
    ADD COLUMN IF NOT EXISTS base_markdown_bucket TEXT,
    ADD COLUMN IF NOT EXISTS base_markdown_object_key TEXT;

CREATE INDEX IF NOT EXISTS transformations_queue_overlay_stage_idx
    ON transformations_queue (overlay_stage)
    WHERE overlay_stage IS NOT NULL;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS transformations_queue_overlay_stage_idx;

ALTER TABLE transformations_queue
    DROP COLUMN base_markdown_object_key,
    DROP COLUMN base_markdown_bucket,
    DROP COLUMN overlay_stage;
-- +goose StatementEnd
