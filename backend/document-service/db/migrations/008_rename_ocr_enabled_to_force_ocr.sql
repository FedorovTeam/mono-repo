-- +goose Up
ALTER TABLE documents
    RENAME COLUMN ocr_enabled TO force_ocr;

-- +goose Down
ALTER TABLE documents
    RENAME COLUMN force_ocr TO ocr_enabled;
