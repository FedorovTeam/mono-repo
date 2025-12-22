-- +goose Up
-- Добавление стадии extract_tables и полей стратегии в documents.
-- +goose StatementBegin
DO $do$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_type WHERE typname = 'job_stage') THEN
        IF NOT EXISTS (
            SELECT 1 FROM pg_enum e
            JOIN pg_type t ON e.enumtypid = t.oid
            WHERE t.typname = 'job_stage' AND e.enumlabel = 'extract_tables'
        ) THEN
            ALTER TYPE job_stage ADD VALUE 'extract_tables';
        END IF;
    END IF;
END;
$do$;
-- +goose StatementEnd

ALTER TABLE documents ADD COLUMN IF NOT EXISTS extract_tables BOOLEAN NOT NULL DEFAULT false;
ALTER TABLE documents ADD COLUMN IF NOT EXISTS conversion_strategy TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE documents DROP COLUMN IF EXISTS conversion_strategy;
ALTER TABLE documents DROP COLUMN IF EXISTS extract_tables;
-- Значение ENUM удалить безопасно нельзя без пересоздания типа.
