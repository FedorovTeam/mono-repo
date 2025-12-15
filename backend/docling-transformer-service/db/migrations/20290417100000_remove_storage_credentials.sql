-- +goose Up
-- +goose StatementBegin
ALTER TABLE transformations_queue
    DROP COLUMN IF EXISTS source_endpoint,
    DROP COLUMN IF EXISTS source_access_key,
    DROP COLUMN IF EXISTS source_secret_key,
    DROP COLUMN IF EXISTS source_use_ssl,
    DROP COLUMN IF EXISTS source_region,
    DROP COLUMN IF EXISTS target_endpoint,
    DROP COLUMN IF EXISTS target_access_key,
    DROP COLUMN IF EXISTS target_secret_key,
    DROP COLUMN IF EXISTS target_use_ssl,
    DROP COLUMN IF EXISTS target_region;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE transformations_queue
    ADD COLUMN IF NOT EXISTS source_endpoint TEXT,
    ADD COLUMN IF NOT EXISTS source_access_key TEXT,
    ADD COLUMN IF NOT EXISTS source_secret_key TEXT,
    ADD COLUMN IF NOT EXISTS source_use_ssl BOOLEAN,
    ADD COLUMN IF NOT EXISTS source_region TEXT,
    ADD COLUMN IF NOT EXISTS target_endpoint TEXT,
    ADD COLUMN IF NOT EXISTS target_access_key TEXT,
    ADD COLUMN IF NOT EXISTS target_secret_key TEXT,
    ADD COLUMN IF NOT EXISTS target_use_ssl BOOLEAN,
    ADD COLUMN IF NOT EXISTS target_region TEXT;
-- +goose StatementEnd
