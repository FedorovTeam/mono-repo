-- +goose Up
-- +goose StatementBegin
ALTER TABLE transformations_queue
    ADD COLUMN IF NOT EXISTS ocr_enabled BOOLEAN,
    ADD COLUMN IF NOT EXISTS ocr_lang TEXT,
    ADD COLUMN IF NOT EXISTS pdf_backend TEXT;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE transformations_queue
    DROP COLUMN IF EXISTS pdf_backend,
    DROP COLUMN IF EXISTS ocr_lang,
    DROP COLUMN IF EXISTS ocr_enabled;
-- +goose StatementEnd
