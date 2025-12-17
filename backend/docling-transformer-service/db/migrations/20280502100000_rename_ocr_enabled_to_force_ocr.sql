-- +goose Up
-- +goose StatementBegin
ALTER TABLE transformations_queue
    RENAME COLUMN IF EXISTS ocr_enabled TO force_ocr;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE transformations_queue
    RENAME COLUMN IF EXISTS force_ocr TO ocr_enabled;
-- +goose StatementEnd
