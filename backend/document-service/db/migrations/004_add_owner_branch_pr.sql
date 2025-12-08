-- +goose Up
ALTER TABLE documents
    ADD COLUMN IF NOT EXISTS owner TEXT,
    ADD COLUMN IF NOT EXISTS base_branch TEXT,
    ADD COLUMN IF NOT EXISTS create_pr BOOLEAN DEFAULT FALSE;

-- +goose Down
ALTER TABLE documents
    DROP COLUMN IF EXISTS owner,
    DROP COLUMN IF EXISTS base_branch,
    DROP COLUMN IF EXISTS create_pr;
