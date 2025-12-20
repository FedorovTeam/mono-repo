CREATE TABLE IF NOT EXISTS table_extraction_queue (
    id TEXT PRIMARY KEY,
    document_id TEXT NOT NULL,
    original_bucket TEXT NOT NULL,
    original_object_key TEXT NOT NULL,
    artifacts_bucket TEXT NOT NULL,
    artifacts_prefix TEXT NOT NULL,
    status TEXT NOT NULL,
    status_reason TEXT DEFAULT '',
    attempts INTEGER NOT NULL DEFAULT 0,
    next_run_at TIMESTAMP NULL,
    finished_at TIMESTAMP NULL,
    error_code TEXT DEFAULT '',
    error_message TEXT DEFAULT '',
    correlation_id TEXT NOT NULL,
    created_at TIMESTAMP NOT NULL,
    updated_at TIMESTAMP NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_table_extraction_queue_status ON table_extraction_queue (status);
CREATE INDEX IF NOT EXISTS idx_table_extraction_queue_document_id ON table_extraction_queue (document_id);
CREATE INDEX IF NOT EXISTS idx_table_extraction_queue_next_run_at ON table_extraction_queue (next_run_at);
CREATE UNIQUE INDEX IF NOT EXISTS uq_table_extraction_queue_active_document ON table_extraction_queue (document_id) WHERE status IN ('pending', 'processing');
