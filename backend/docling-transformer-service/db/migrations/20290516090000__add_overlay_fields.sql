-- +goose Up
-- +goose StatementBegin
ALTER TABLE transformations_queue
    ADD COLUMN overlay_stage TEXT,
    ADD COLUMN base_markdown_bucket TEXT,
    ADD COLUMN base_markdown_object_key TEXT;

CREATE INDEX transformations_queue_overlay_stage_idx
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
