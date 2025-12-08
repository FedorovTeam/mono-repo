-- +goose Up
ALTER TABLE documents
    ADD COLUMN IF NOT EXISTS repository TEXT,
    ADD COLUMN IF NOT EXISTS branch TEXT,
    ADD COLUMN IF NOT EXISTS path TEXT,
    ADD COLUMN IF NOT EXISTS error_reason TEXT,
    ADD COLUMN IF NOT EXISTS workflow_id TEXT;

ALTER TABLE document_status_logs
    ADD COLUMN IF NOT EXISTS workflow_id TEXT;

-- +goose Down
ALTER TABLE document_status_logs
    DROP COLUMN IF EXISTS workflow_id;

ALTER TABLE documents
    DROP COLUMN IF EXISTS repository,
    DROP COLUMN IF EXISTS branch,
    DROP COLUMN IF EXISTS path,
    DROP COLUMN IF EXISTS error_reason,
    DROP COLUMN IF EXISTS workflow_id;
