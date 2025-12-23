-- +goose Up
ALTER TABLE transformations_queue
    ADD COLUMN IF NOT EXISTS table_metadata JSONB;

-- +goose Down
ALTER TABLE transformations_queue
    DROP COLUMN IF EXISTS table_metadata;
