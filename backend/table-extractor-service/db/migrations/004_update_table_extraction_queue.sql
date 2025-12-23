-- +goose Up
-- +goose StatementBegin
ALTER TABLE table_extraction_queue RENAME COLUMN id TO queue_id;
ALTER TABLE table_extraction_queue RENAME COLUMN error_message TO error;
ALTER TABLE table_extraction_queue ADD COLUMN markdown_bucket TEXT DEFAULT '';
ALTER TABLE table_extraction_queue ADD COLUMN markdown_object_key TEXT DEFAULT '';
CREATE INDEX IF NOT EXISTS idx_table_extraction_queue_queue_id ON table_extraction_queue (queue_id);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE table_extraction_queue RENAME COLUMN queue_id TO id;
ALTER TABLE table_extraction_queue RENAME COLUMN error TO error_message;
ALTER TABLE table_extraction_queue DROP COLUMN markdown_object_key;
ALTER TABLE table_extraction_queue DROP COLUMN markdown_bucket;
DROP INDEX IF EXISTS idx_table_extraction_queue_queue_id;
-- +goose StatementEnd
