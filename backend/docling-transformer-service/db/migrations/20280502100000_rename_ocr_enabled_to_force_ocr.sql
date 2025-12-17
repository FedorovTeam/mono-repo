-- +goose Up
-- +goose StatementBegin
DO $$
BEGIN
    IF EXISTS (
        SELECT 1
        FROM information_schema.columns
        WHERE table_name = 'transformations_queue'
          AND column_name = 'ocr_enabled'
    ) THEN
ALTER TABLE transformations_queue
    RENAME COLUMN ocr_enabled TO force_ocr;
END IF;
END $$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DO $$
BEGIN
    IF EXISTS (
        SELECT 1
        FROM information_schema.columns
        WHERE table_name = 'transformations_queue'
          AND column_name = 'force_ocr'
    ) THEN
ALTER TABLE transformations_queue
    RENAME COLUMN force_ocr TO ocr_enabled;
END IF;
END $$;
-- +goose StatementEnd
