-- +goose Up
ALTER TABLE preprocess_queue
    ADD COLUMN IF NOT EXISTS document_id TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE preprocess_queue
    DROP COLUMN IF EXISTS document_id;
