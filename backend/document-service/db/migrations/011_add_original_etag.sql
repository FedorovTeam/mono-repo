-- +goose Up
ALTER TABLE document_versions
    ADD COLUMN IF NOT EXISTS original_etag TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE document_versions
    DROP COLUMN IF EXISTS original_etag;
