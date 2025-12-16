-- +goose Up
ALTER TABLE documents
    ADD COLUMN IF NOT EXISTS ocr_enabled BOOLEAN NOT NULL DEFAULT false;

-- +goose Down
ALTER TABLE documents
    DROP COLUMN IF EXISTS ocr_enabled;
