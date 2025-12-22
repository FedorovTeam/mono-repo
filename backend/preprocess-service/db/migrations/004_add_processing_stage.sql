-- +goose Up
-- Добавляет этап обработки и отметки времени для heartbeat.

-- +goose StatementBegin
DO $do$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_type WHERE typname = 'processing_stage') THEN
        CREATE TYPE processing_stage AS ENUM (
            'downloading',
            'converting',
            'uploading',
            'callback'
        );
    END IF;
END;
$do$;
-- +goose StatementEnd

ALTER TABLE preprocess_queue
    ADD COLUMN IF NOT EXISTS processing_stage processing_stage NOT NULL DEFAULT 'downloading',
    ADD COLUMN IF NOT EXISTS stage_started_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS stage_updated_at TIMESTAMPTZ NOT NULL DEFAULT now();

UPDATE preprocess_queue
SET stage_started_at = COALESCE(stage_started_at, started_at, created_at, now()),
    stage_updated_at = COALESCE(stage_updated_at, updated_at, now())
WHERE stage_updated_at IS NULL OR processing_stage IS NULL;

-- +goose Down
ALTER TABLE preprocess_queue
    DROP COLUMN IF EXISTS stage_updated_at,
    DROP COLUMN IF EXISTS stage_started_at,
    DROP COLUMN IF EXISTS processing_stage;

DROP TYPE IF EXISTS processing_stage;
