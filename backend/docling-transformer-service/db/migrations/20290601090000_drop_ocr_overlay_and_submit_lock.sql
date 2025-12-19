-- +goose Up
-- +goose StatementBegin
DROP INDEX IF EXISTS transformations_queue_overlay_stage_idx;

ALTER TABLE transformations_queue
    DROP COLUMN IF EXISTS force_ocr,
    DROP COLUMN IF EXISTS ocr_lang,
    DROP COLUMN IF EXISTS pdf_backend,
    DROP COLUMN IF EXISTS base_markdown_object_key,
    DROP COLUMN IF EXISTS base_markdown_bucket,
    DROP COLUMN IF EXISTS overlay_stage;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE IF EXISTS docling_submit_lock;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE transformations_queue
    ADD COLUMN IF NOT EXISTS overlay_stage TEXT,
    ADD COLUMN IF NOT EXISTS base_markdown_bucket TEXT,
    ADD COLUMN IF NOT EXISTS base_markdown_object_key TEXT,
    ADD COLUMN IF NOT EXISTS pdf_backend TEXT,
    ADD COLUMN IF NOT EXISTS ocr_lang TEXT,
    ADD COLUMN IF NOT EXISTS force_ocr BOOLEAN;

CREATE INDEX IF NOT EXISTS transformations_queue_overlay_stage_idx
    ON transformations_queue (overlay_stage)
    WHERE overlay_stage IS NOT NULL;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS docling_submit_lock (
    token text PRIMARY KEY,
    updated_at timestamptz NOT NULL DEFAULT NOW()
);

INSERT INTO docling_submit_lock (token) VALUES ('docling_submit')
ON CONFLICT (token) DO NOTHING;
-- +goose StatementEnd
