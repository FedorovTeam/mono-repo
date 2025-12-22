-- +goose Up
-- +goose StatementBegin
DO $do$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_type WHERE typname = 'job_stage') THEN
        IF NOT EXISTS (
            SELECT 1 FROM pg_enum e
            JOIN pg_type t ON e.enumtypid = t.oid
            WHERE t.typname = 'job_stage' AND e.enumlabel = 'preprocess_to_docx'
        ) THEN
            ALTER TYPE job_stage ADD VALUE 'preprocess_to_docx';
        END IF;
    END IF;
END;
$do$;
-- +goose StatementEnd

-- +goose Down
-- Нельзя безопасно удалить значение ENUM без пересоздания типа, поэтому откат не выполняется.
