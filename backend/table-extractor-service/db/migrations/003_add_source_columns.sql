-- +goose Up
-- +goose StatementBegin
ALTER TABLE table_extraction_jobs
    ADD COLUMN source_url TEXT DEFAULT '';

ALTER TABLE table_extraction_jobs
    ADD COLUMN local_path TEXT DEFAULT '';

ALTER TABLE table_extraction_queue
    ADD COLUMN source_url TEXT DEFAULT '';

ALTER TABLE table_extraction_queue
    ADD COLUMN local_path TEXT DEFAULT '';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE table_extraction_queue
    DROP COLUMN IF EXISTS local_path,
    DROP COLUMN IF EXISTS source_url;

ALTER TABLE table_extraction_jobs
    DROP COLUMN IF EXISTS local_path,
    DROP COLUMN IF EXISTS source_url;
-- +goose StatementEnd
