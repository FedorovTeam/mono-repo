-- +goose Up
ALTER TABLE documents ADD COLUMN IF NOT EXISTS workflow_id TEXT;

-- +goose Down
ALTER TABLE documents DROP COLUMN IF EXISTS workflow_id;
