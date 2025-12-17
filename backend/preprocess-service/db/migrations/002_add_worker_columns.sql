-- +goose Up
ALTER TABLE preprocess_queue
    ADD COLUMN IF NOT EXISTS correlation_id TEXT,
    ADD COLUMN IF NOT EXISTS target_media_type TEXT;

-- +goose Down
ALTER TABLE preprocess_queue
    DROP COLUMN IF EXISTS target_media_type,
    DROP COLUMN IF EXISTS correlation_id;
