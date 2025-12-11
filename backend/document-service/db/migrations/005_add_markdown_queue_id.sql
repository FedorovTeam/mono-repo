-- +goose Up
ALTER TABLE document_versions
    ADD COLUMN IF NOT EXISTS markdown_queue_id TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE document_versions
    DROP COLUMN IF EXISTS markdown_queue_id;
