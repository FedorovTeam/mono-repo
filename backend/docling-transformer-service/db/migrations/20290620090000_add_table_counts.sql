-- +goose Up
ALTER TABLE transformations_queue
    ADD COLUMN IF NOT EXISTS table_count INT,
    ADD COLUMN IF NOT EXISTS table_pages INT[];

-- +goose Down
ALTER TABLE transformations_queue
    DROP COLUMN IF EXISTS table_pages,
    DROP COLUMN IF EXISTS table_count;
