CREATE TABLE IF NOT EXISTS table_extraction_jobs (
    id TEXT PRIMARY KEY,
    document_id TEXT NOT NULL UNIQUE,
    status TEXT NOT NULL,
    status_reason TEXT DEFAULT '',
    raw_json_bucket TEXT NOT NULL,
    raw_json_key TEXT NOT NULL,
    normalized_json_bucket TEXT NOT NULL,
    normalized_json_key TEXT NOT NULL,
    markdown_bucket TEXT NOT NULL,
    markdown_key TEXT NOT NULL,
    manifest_bucket TEXT NOT NULL,
    manifest_key TEXT NOT NULL,
    debug_bucket TEXT NOT NULL,
    debug_key TEXT NOT NULL,
    correlation_id TEXT NOT NULL,
    created_at TIMESTAMP NOT NULL,
    updated_at TIMESTAMP NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_table_extraction_jobs_status ON table_extraction_jobs (status);
