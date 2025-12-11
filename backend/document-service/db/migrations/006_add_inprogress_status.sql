-- +goose Up
-- Добавляем статус InProgress для отображения активного запуска workflow.
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_enum WHERE enumlabel = 'InProgress' AND enumtypid = (
            SELECT oid FROM pg_type WHERE typname = 'document_status'
        )
    ) THEN
        ALTER TYPE document_status ADD VALUE 'InProgress';
    END IF;
END;
$$;

-- +goose Down
-- Удаляем статус InProgress, пересоздавая enum без него.
DO $$
BEGIN
    IF EXISTS (
        SELECT 1 FROM pg_enum WHERE enumlabel = 'InProgress' AND enumtypid = (
            SELECT oid FROM pg_type WHERE typname = 'document_status'
        )
    ) THEN
        CREATE TYPE document_status_old AS ENUM (
            'Uploaded',
            'StoredOriginal',
            'ConvertedToMarkdown',
            'PublishedToGitHub',
            'Failed'
        );

        ALTER TABLE documents ALTER COLUMN status TYPE document_status_old USING status::text::document_status_old;
        ALTER TABLE document_status_logs ALTER COLUMN from_status TYPE document_status_old USING from_status::text::document_status_old;
        ALTER TABLE document_status_logs ALTER COLUMN to_status TYPE document_status_old USING to_status::text::document_status_old;

        DROP TYPE document_status;
        ALTER TYPE document_status_old RENAME TO document_status;
    END IF;
END;
$$;
