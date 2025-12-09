CREATE TABLE IF NOT EXISTS transformations_queue (
    id UUID PRIMARY KEY,
    status TEXT NOT NULL CHECK (status IN ('pending', 'processing', 'done', 'failed')),
    document_id TEXT NOT NULL,
    original_bucket TEXT NOT NULL,
    original_object_key TEXT NOT NULL,
    markdown_bucket TEXT NOT NULL,
    markdown_object_key TEXT NOT NULL,
    filename_extension TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    started_at TIMESTAMPTZ,
    finished_at TIMESTAMPTZ,
    error TEXT
);

CREATE INDEX IF NOT EXISTS transformations_queue_status_idx ON transformations_queue(status, created_at);
