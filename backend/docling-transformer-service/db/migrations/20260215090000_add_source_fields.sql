-- +goose Up
-- +goose StatementBegin
ALTER TABLE transformations_queue
    ADD COLUMN IF NOT EXISTS source_bucket TEXT,
    ADD COLUMN IF NOT EXISTS source_object_key TEXT,
    ADD COLUMN IF NOT EXISTS source_url TEXT;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE transformations_queue
    DROP COLUMN IF EXISTS source_url,
    DROP COLUMN IF EXISTS source_object_key,
    DROP COLUMN IF EXISTS source_bucket;
-- +goose StatementEnd
