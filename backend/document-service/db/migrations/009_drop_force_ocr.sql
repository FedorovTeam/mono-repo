-- +goose Up
ALTER TABLE documents
    DROP COLUMN IF EXISTS force_ocr;

-- +goose Down
ALTER TABLE documents
    ADD COLUMN IF NOT EXISTS force_ocr BOOLEAN NOT NULL DEFAULT false;
